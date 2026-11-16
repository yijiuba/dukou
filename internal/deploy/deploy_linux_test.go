package deploy

import (
	"encoding/base64"
	"strings"
	"testing"
)

// TestWriteFileCommand 确认文件内容能原样还原。
// 用 base64 的唯一理由是避开引号地狱——一旦这里出 bug，
// 写进服务器的 systemd 单元就会带垃圾字符，服务起不来且报错莫名其妙。
func TestWriteFileCommand(t *testing.T) {
	content := "[Unit]\nDescription=a'b\"c $HOME `id`\nExecStart=/usr/bin/gost -L http+tls://u:p@:8443\n"

	cmd := writeFileCommand("/etc/systemd/system/x.service", content, false)
	if !strings.HasPrefix(cmd, "echo '") {
		t.Fatalf("命令格式不对: %s", cmd)
	}

	// 抠出 base64 段并还原
	start := strings.Index(cmd, "echo '") + len("echo '")
	end := strings.Index(cmd, "' | base64 -d")
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("无法从命令中定位 base64 段: %s", cmd)
	}
	raw, err := base64.StdEncoding.DecodeString(cmd[start:end])
	if err != nil {
		t.Fatalf("base64 解码失败: %v", err)
	}
	if string(raw) != content {
		t.Fatalf("内容不一致\n得到: %q\n期望: %q", string(raw), content)
	}
	if !strings.Contains(cmd, "/etc/systemd/system/x.service") {
		t.Error("命令里缺少目标路径")
	}
}

// TestWriteFileCommandSudo 确认需要 root 时走 tee。
func TestWriteFileCommandSudo(t *testing.T) {
	cmd := writeFileCommand("/etc/x", "y", true)
	if !strings.Contains(cmd, "sudo tee") {
		t.Fatalf("应以 sudo tee 写入: %s", cmd)
	}
}

// TestWithDefaults 确认高级项留空时给出可用默认值。
func TestWithDefaults(t *testing.T) {
	r := withDefaults(Request{ServerHost: "1.2.3.4"})
	if r.SSHPort != 22 {
		t.Errorf("SSH 端口默认值应为 22，实际 %d", r.SSHPort)
	}
	if r.SSHUser != "root" {
		t.Errorf("SSH 用户默认值应为 root，实际 %q", r.SSHUser)
	}
	if r.Port != 8443 {
		t.Errorf("代理端口默认值应为 8443，实际 %d", r.Port)
	}
	if r.User != "dukou" {
		t.Errorf("代理用户名默认值应为 dukou，实际 %q", r.User)
	}
	if len(r.Password) != 16 {
		t.Errorf("应自动生成 16 位密码，实际 %q(%d)", r.Password, len(r.Password))
	}
}

// TestWithDefaultsKeepsGiven 确认显式填写的值不会被覆盖。
func TestWithDefaultsKeepsGiven(t *testing.T) {
	r := withDefaults(Request{SSHPort: 2222, Port: 9443, User: "me", Password: "explicit1234"})
	if r.SSHPort != 2222 || r.Port != 9443 || r.User != "me" || r.Password != "explicit1234" {
		t.Fatalf("显式值被覆盖: %+v", r)
	}
}

// TestProxyEnvBlock 确认环境变量块有唯一标记、且包含国内免代理列表。
// 没有 NO_PROXY 里的 .aliyuncs.com，apt 会绕到海外再回来，直接 503。
func TestProxyEnvBlock(t *testing.T) {
	b := proxyEnvBlock()
	for _, want := range []string{
		">>> dukou proxy >>>", "<<< dukou proxy <<<",
		"HTTPS_PROXY", "http://127.0.0.1:7890",
		"NO_PROXY", ".aliyuncs.com", ".cn",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("环境变量块缺少 %q", want)
		}
	}
}

// TestGnomeProxyScript 确认系统代理指向本机端口且带忽略列表。
func TestGnomeProxyScript(t *testing.T) {
	s := gnomeProxyScript(7890)
	for _, want := range []string{
		"org.gnome.system.proxy mode 'manual'",
		"org.gnome.system.proxy.http port 7890",
		"org.gnome.system.proxy.https port 7890",
		"org.gnome.system.proxy.socks port 7891",
		"ignore-hosts",
		"127.0.0.0/8",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("系统代理脚本缺少 %q", want)
		}
	}
}

// TestClientUnitGoesThroughTLS 是一条回归测试。
//
// 客户端必须用 -F tls://，写成 http+tls:// 会让整条链路 503；
// 本地监听必须绑 127.0.0.1，绑 0.0.0.0 等于把无鉴权代理送给整个局域网。
func TestClientUnitGoesThroughTLS(t *testing.T) {
	req := Request{ServerHost: "47.1.2.3", Port: 8443, User: "u", Password: "p"}
	u := clientUnit(req, "/usr/local/bin/gost")

	if !strings.Contains(u.ExecStart, "-F tls://") {
		t.Errorf("必须用 -F tls://：%s", u.ExecStart)
	}
	if strings.Contains(u.ExecStart, "http+tls://47") {
		t.Errorf("客户端不能用 http+tls://：%s", u.ExecStart)
	}
	if !strings.Contains(u.ExecStart, "-L http://127.0.0.1:7890") ||
		!strings.Contains(u.ExecStart, "-L socks5://127.0.0.1:7891") {
		t.Errorf("本地监听必须绑定 127.0.0.1：%s", u.ExecStart)
	}
}

// TestServerUnitUsesHTTPOverTLS 确认服务端只开加密口。
func TestServerUnitUsesHTTPOverTLS(t *testing.T) {
	req := Request{Port: 8443, User: "u", Password: "p"}
	u := serverUnit(req, "/usr/bin/gost")
	if !strings.Contains(u.ExecStart, "-L http+tls://u:p@:8443") {
		t.Errorf("服务端应监听 http+tls：%s", u.ExecStart)
	}
}

// TestShellQuote 确认单引号被正确转义，避免命令注入或截断。
func TestShellQuote(t *testing.T) {
	if got := shellQuote("abc"); got != "'abc'" {
		t.Errorf("shellQuote(abc)=%q", got)
	}
	if got := shellQuote("a'b"); got != `'a'\''b'` {
		t.Errorf("单引号转义不对: %q", got)
	}
}

// TestNodeMajor 确认 Node 版本解析，">=18" 是 Codex 的硬要求。
func TestNodeMajor(t *testing.T) {
	cases := map[string]int{"v20.11.1": 20, "v18.0.0": 18, "v16.20.2": 16, "none": 0, "": 0}
	for in, want := range cases {
		if got := nodeMajor(in); got != want {
			t.Errorf("nodeMajor(%q)=%d，期望 %d", in, got, want)
		}
	}
}
