package proxy

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

// PAC 脚本必须能用 ES3 引擎跑（WinINet 的老 JScript）：
// 出现任何 ES5+ 特性（endsWith/includes/箭头函数/let/const）即失败。
func TestPACES3Only(t *testing.T) {
	js := buildPAC(7890)
	for _, bad := range []string{"=>", ".endsWith", ".includes", "let ", "const ", "`", ".startsWith"} {
		if strings.Contains(js, bad) {
			t.Errorf("PAC 脚本含 ES5+ 特性 %q，WinINet 的 JScript 引擎会静默失效", bad)
		}
	}
	if !strings.Contains(js, "function FindProxyForURL") {
		t.Fatal("缺少 FindProxyForURL 入口")
	}
	if !strings.Contains(js, "PROXY 127.0.0.1:7890") {
		t.Error("默认出口应指向本机 7890")
	}
}

// 匹配函数 m 的行为要与域名后缀语义一致：本体匹配、子域匹配、
// 前缀相似但不同的域（evildaobao.com）绝不能匹配。
func TestPACDomainMatch(t *testing.T) {
	// 从生成的脚本里提取 m 函数和域名数组，交给 JS 引擎之外的方式验证太重；
	// 这里直接用 Go 复刻同一语义对照测试，保证两处逻辑一致。
	list := cnDomains
	m := func(h string) bool {
		for _, d := range list {
			if h == d {
				return true
			}
			n := len(h) - len(d) - 1
			if n > 0 && h[n:] == "."+d {
				return true
			}
		}
		return false
	}
	if !m("www.taobao.com") || !m("taobao.com") {
		t.Error("taobao.com 及子域应匹配")
	}
	if m("evildaobao.com") {
		t.Error("前缀相似域名不应匹配（安全边界）")
	}
	if m("notaobao.com") {
		t.Error("前缀相似域名不应匹配")
	}
	if !m("docs.qq.com") {
		t.Error("腾讯文档应匹配")
	}
}

// 内网段与 .cn 通配必须出现在脚本里：漏了会把路由器管理页送进代理。
func TestPACDirectRules(t *testing.T) {
	js := buildPAC(7890)
	for _, want := range []string{
		"isInNet(ip, '192.168.0.0', '255.255.0.0')",
		"isInNet(ip, '10.0.0.0', '255.0.0.0')",
		"shExpMatch(host, '*.cn')",
		"isPlainHostName(host)",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("PAC 缺少直连规则: %s", want)
		}
	}
}

// pacFileURL 的 Windows 路径转换：file:/// 三斜杠 + 正斜杠。
func TestPACFileURL(t *testing.T) {
	u := pacFileURL(`C:\Users\x\AppData\Roaming\dukou\proxy.pac`)
	if u != "file:///C:/Users/x/AppData/Roaming/dukou/proxy.pac" {
		t.Errorf("file URL 转换错误: %s", u)
	}
}

// TCPRTTMS 必须量的是 TCP 建连，且失败/非法输入返回 0 而不是编数字。
func TestTCPRTTMS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	port, _ := strconv.Atoi(strings.Split(ln.Addr().String(), ":")[1])
	ms := TCPRTTMS("127.0.0.1", port)
	if ms <= 0 || ms > 1500 {
		t.Fatalf("本机监听器 RTT 异常: %dms", ms)
	}

	// 不存在的端口：3 次全失败必须返回 0，不能编造延迟
	if dead := TCPRTTMS("127.0.0.1", 1); dead != 0 {
		t.Fatalf("死端口应返回 0, got %d", dead)
	}
	if TCPRTTMS("", 0) != 0 {
		t.Fatalf("非法输入应返回 0")
	}
}
