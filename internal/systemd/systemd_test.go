package systemd

import (
	"strings"
	"testing"
)

func TestServiceName(t *testing.T) {
	if got := ServiceName("server"); got != "dukou" {
		t.Errorf("服务端服务名应为 dukou，实际 %s", got)
	}
	if got := ServiceName("client"); got != "dukou-client" {
		t.Errorf("客户端服务名应为 dukou-client，实际 %s", got)
	}
}

func TestUnitPath(t *testing.T) {
	u := Unit{Name: "dukou"}
	if got := u.Path(); got != "/etc/systemd/system/dukou.service" {
		t.Errorf("路径错误: %s", got)
	}
}

func TestServerUnitExecStart(t *testing.T) {
	u := ServerUnit("/usr/bin/gost", 8443, "dukou", "Secret123")
	want := "/usr/bin/gost -L http+tls://dukou:Secret123@:8443"
	if u.ExecStart != want {
		t.Errorf("ExecStart 错误:\n  got  = %s\n  want = %s", u.ExecStart, want)
	}
	if u.Name != "dukou" {
		t.Errorf("服务名应为 dukou，实际 %s", u.Name)
	}
}

// 这条测试守着一个关键设计：服务端必须是 http+tls，
// 不能退化成明文 http——否则 google/chatgpt 这类被封锁的域名会立即被 RST。
func TestServerUnitUsesTLS(t *testing.T) {
	u := ServerUnit("/usr/bin/gost", 8443, "u", "p")
	if !strings.Contains(u.ExecStart, "http+tls://") {
		t.Fatalf("服务端必须用 http+tls 监听，实际: %s", u.ExecStart)
	}
	if strings.Contains(u.ExecStart, "-L http://") {
		t.Fatalf("服务端不得出现明文 http 监听，实际: %s", u.ExecStart)
	}
}

func TestClientUnitExecStart(t *testing.T) {
	u := ClientUnit("/usr/bin/gost", "1.2.3.4", 8443, "dukou", "Secret123", 7890, 7891)
	want := "/usr/bin/gost -L http://127.0.0.1:7890 -L socks5://127.0.0.1:7891 " +
		"-F tls://dukou:Secret123@1.2.3.4:8443"
	if u.ExecStart != want {
		t.Errorf("ExecStart 错误:\n  got  = %s\n  want = %s", u.ExecStart, want)
	}
}

// 客户端本地监听必须只绑定 loopback，且上游必须用 tls://。
// 这两点各对应一次实际踩坑：前者是安全底线，后者写错会全站 503。
func TestClientUnitLocalOnlyAndTLSUpstream(t *testing.T) {
	u := ClientUnit("/usr/bin/gost", "1.2.3.4", 8443, "u", "p", 7890, 7891)

	if strings.Contains(u.ExecStart, "http://:7890") ||
		strings.Contains(u.ExecStart, "0.0.0.0") {
		t.Errorf("本地监听不应暴露到 0.0.0.0: %s", u.ExecStart)
	}
	if !strings.Contains(u.ExecStart, "-F tls://") {
		t.Errorf("上游必须用 tls://（写成 http+tls:// 会导致全站 503）: %s", u.ExecStart)
	}
	if strings.Contains(u.ExecStart, "-F http+tls://") {
		t.Errorf("上游写成 http+tls:// 是错的: %s", u.ExecStart)
	}
}

func TestRenderContainsRequiredDirectives(t *testing.T) {
	u := ServerUnit("/usr/bin/gost", 8443, "u", "p")
	out := string(u.Render())

	required := []string{
		"[Unit]",
		"[Service]",
		"[Install]",
		"Type=simple",
		"Restart=always",
		"RestartSec=3",
		"LimitNOFILE=65535",
		"WantedBy=multi-user.target",
		"After=network-online.target",
	}
	for _, r := range required {
		if !strings.Contains(out, r) {
			t.Errorf("单元文件缺少 %q", r)
		}
	}
}

func TestRenderIncludesExtraLines(t *testing.T) {
	u := Unit{
		Name:        "t",
		Description: "test",
		ExecStart:   "/bin/true",
		ExtraUnit:   []string{"After=docker.service"},
		ExtraSvc:    []string{"Environment=FOO=bar"},
	}
	out := string(u.Render())

	if !strings.Contains(out, "After=docker.service") {
		t.Error("ExtraUnit 未渲染")
	}
	if !strings.Contains(out, "Environment=FOO=bar") {
		t.Error("ExtraSvc 未渲染")
	}
}

func TestEnableDisableCommands(t *testing.T) {
	u := ServerUnit("/usr/bin/gost", 8443, "u", "p")

	enable := u.EnableCommands()
	if len(enable) != 2 {
		t.Fatalf("启用命令应为 2 条，实际 %d", len(enable))
	}
	if enable[0][1] != "daemon-reload" {
		t.Errorf("第一条应为 daemon-reload，实际 %v", enable[0])
	}
	if enable[1][3] != "dukou" {
		t.Errorf("第二条应启用 dukou，实际 %v", enable[1])
	}

	disable := u.DisableCommands()
	if disable[0][1] != "disable" {
		t.Errorf("停用时应先 disable，实际 %v", disable[0])
	}
}
