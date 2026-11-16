//go:build windows && manual

package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yijiuba/dukou/internal/store"
)

// TestManualServiceConnect 走完整链路：落库 → 加密存密码 → 连接 → 断开 → 还原。
//
// 这是 GUI 上那个「连接」按钮背后真正发生的事，只是少了一层点击。
// 需要真实服务器：
//
//	DUKOU_VERIFY_HOST=1.2.3.4 DUKOU_VERIFY_PASS=xxx \
//	  go test -tags manual -run TestManualServiceConnect ./internal/app/ -v -timeout 5m
func TestManualServiceConnect(t *testing.T) {
	host := os.Getenv("DUKOU_VERIFY_HOST")
	pass := os.Getenv("DUKOU_VERIFY_PASS")
	if host == "" || pass == "" {
		t.Skip("未设置 DUKOU_VERIFY_HOST / DUKOU_VERIFY_PASS")
	}

	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Startup(context.Background())
	defer s.Shutdown()

	// 复用真实配置目录里已下载好的 gost.exe，避免测试再去下一遍 17 MB
	if base, err := os.UserConfigDir(); err == nil {
		src := filepath.Join(base, "dukou", "bin", "gost.exe")
		if data, err := os.ReadFile(src); err == nil {
			dst := filepath.Join(dir, "bin", "gost.exe")
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err == nil {
				if err := os.WriteFile(dst, data, 0o755); err != nil {
					t.Fatalf("准备 gost.exe 失败: %v", err)
				}
			}
		}
	}

	srv, err := s.AddServer(store.ServerInput{
		Name:     "验证线路",
		Host:     host,
		SSHUser:  "root",
		AuthType: "password",
		SSHPort:  22,
		// SSH 密码这里用不到：连接只读代理密码。
		// 真实场景下这台机器的 root 密码也不会交给客户端。
		SSHPassword:   "unused-on-connect",
		ProxyPassword: pass,
		ServerPort:    8443,
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}
	if srv.ServerPort != 8443 {
		t.Errorf("ServerPort 未保存: %d", srv.ServerPort)
	}

	defer func() {
		if err := s.Disconnect(); err != nil {
			t.Logf("收尾断开: %v", err)
		}
	}()

	st, err := s.Connect(srv.ID)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !st.Connected {
		t.Fatal("连接后 Status.Connected 应为 true")
	}
	if st.EgressIP != host {
		t.Errorf("出口 IP = %q，期望 %q", st.EgressIP, host)
	}
	t.Logf("服务层连接成功：本地 %d，出口 %s，往返 %d ms", st.LocalPort, st.EgressIP, st.LatencyMS)

	// 凭据只进加密库：清单里不能出现明文密码
	raw, err := os.ReadFile(filepath.Join(dir, "servers.json"))
	if err == nil {
		if containsStr(string(raw), pass) {
			t.Fatal("servers.json 里出现了明文代理密码")
		}
	}

	cur, err := s.ProxyStatus()
	if err != nil {
		t.Fatalf("ProxyStatus: %v", err)
	}
	if !cur.Connected || cur.Residue {
		t.Errorf("ProxyStatus 应显示已连接: %+v", cur)
	}
}

func containsStr(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
