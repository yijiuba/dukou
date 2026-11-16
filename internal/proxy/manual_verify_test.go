//go:build windows && manual

package proxy

import (
	"os"
	"path/filepath"
	"testing"
)

// TestManualLifecycle 在真实机器上验证完整生命周期：连接 → 接管系统代理 → 断开 → 还原。
//
// 需要真实服务器凭据，所以放在 manual tag 下，默认不跑：
//
//	DUKOU_VERIFY_HOST=1.2.3.4 DUKOU_VERIFY_PASS=xxx \
//	  go test -tags manual -run TestManualLifecycle ./internal/proxy/ -v -timeout 5m
//
// 它必须一次跑完整个来回：执行环境会在命令结束时回收进程树，
// 分几次调用无法观察到「常驻进程还在」这件事。
func TestManualLifecycle(t *testing.T) {
	host := os.Getenv("DUKOU_VERIFY_HOST")
	pass := os.Getenv("DUKOU_VERIFY_PASS")
	if host == "" || pass == "" {
		t.Skip("未设置 DUKOU_VERIFY_HOST / DUKOU_VERIFY_PASS")
	}
	user := os.Getenv("DUKOU_VERIFY_USER")
	if user == "" {
		user = "dukou"
	}

	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	o := Options{DataDir: filepath.Join(base, "dukou")}

	before, err := readSystemProxy()
	if err != nil {
		t.Fatalf("读取原始代理失败: %v", err)
	}
	t.Logf("连接前系统代理: enable=%d server=%q", before.ProxyEnable, before.ProxyServer)

	// 无论测试成败，都要把系统代理还原，别把开发机留在坏状态
	defer func() {
		if err := Stop(o); err != nil {
			t.Errorf("收尾还原失败: %v", err)
		}
		after, _ := readSystemProxy()
		if after.ProxyServer != before.ProxyServer || after.ProxyEnable != before.ProxyEnable {
			t.Errorf("系统代理未严格还原：期望 %q/%d，实际 %q/%d",
				before.ProxyServer, before.ProxyEnable, after.ProxyServer, after.ProxyEnable)
		} else {
			t.Logf("系统代理已严格还原为 %q", after.ProxyServer)
		}
	}()

	up := Upstream{
		Host:     host,
		Port:     8443,
		User:     user,
		Password: pass,
	}
	st, err := Start(up, o, nil)
	if err != nil {
		t.Fatalf("启动代理失败: %v", err)
	}
	if !st.Connected {
		t.Fatal("Status.Connected 应为 true")
	}
	if st.EgressIP != host {
		t.Errorf("出口 IP = %q，期望 %q", st.EgressIP, host)
	}
	t.Logf("连接成功：本地 %d，出口 %s，往返 %d ms", st.LocalPort, st.EgressIP, st.LatencyMS)

	// 系统代理必须真的被接管：PAC 模式下看 AutoConfigURL，
	// 而不是固定代理的 ProxyServer（那个值保持用户原样作兜底）。
	if !SystemPACIsOurs(o) {
		t.Error("注册表 AutoConfigURL 未指向本工具的 PAC 文件")
	}

	// PAC 分流模式：AutoConfigURL 必须指向本工具生成的脚本，
	// 且 ProxyEnable 不再是我们写的 1（PAC 优先级高于固定代理，
	// 保留用户原来的固定代理当兜底）。
	pacPath, pacURL, err := WritePAC(o, st.LocalPort)
	if err != nil {
		t.Fatalf("写 PAC 失败: %v", err)
	}
	if got := pacURL; !SystemPACIsOurs(o) {
		t.Errorf("AutoConfigURL 未指向 PAC 文件 %s", got)
	} else {
		t.Logf("系统 PAC 已接管: %s", pacURL)
	}
	if _, err := os.Stat(pacPath); err != nil {
		t.Errorf("PAC 文件不存在: %v", err)
	}

	// Current 应当读到同样的状态
	cur, err := Current(o)
	if err != nil {
		t.Fatalf("Current 失败: %v", err)
	}
	if !cur.Connected || cur.Residue {
		t.Errorf("Current 状态异常: %+v", cur)
	}
}

// TestManualReconnect 验证「连两次再断开」仍然回到最初配置。
//
// 这是最容易被忽略的路径：第二次连接时注册表里已经是我们写的 7890，
// 如果这时重新快照，真正的原值（用户的 7897）就被永久吞掉了。
// 断开后用户会发现代理配置消失，且完全不知道为什么。
func TestManualReconnect(t *testing.T) {
	host := os.Getenv("DUKOU_VERIFY_HOST")
	pass := os.Getenv("DUKOU_VERIFY_PASS")
	if host == "" || pass == "" {
		t.Skip("未设置 DUKOU_VERIFY_HOST / DUKOU_VERIFY_PASS")
	}
	user := os.Getenv("DUKOU_VERIFY_USER")
	if user == "" {
		user = "dukou"
	}

	base, _ := os.UserConfigDir()
	o := Options{DataDir: filepath.Join(base, "dukou")}

	before, _ := readSystemProxy()
	t.Logf("最初配置: %q", before.ProxyServer)

	defer func() {
		_ = Stop(o)
		after, _ := readSystemProxy()
		if after.ProxyServer != before.ProxyServer {
			t.Errorf("重复连接后未还原到最初配置：期望 %q，实际 %q",
				before.ProxyServer, after.ProxyServer)
		} else {
			t.Logf("重复连接后仍正确还原为 %q", after.ProxyServer)
		}
	}()

	up := Upstream{Host: host, Port: 8443, User: user, Password: pass}
	for i := 0; i < 2; i++ {
		if _, err := Start(up, o, nil); err != nil {
			t.Fatalf("第 %d 次连接失败: %v", i+1, err)
		}
		t.Logf("第 %d 次连接成功", i+1)
	}
}
