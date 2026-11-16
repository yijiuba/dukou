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

// TestManualServiceDoctor 对真实服务器跑完整体检（当前线路场景）。
//
//	DUKOU_VERIFY_HOST=1.2.3.4 DUKOU_VERIFY_PASS=代理密码 DUKOU_VERIFY_ROOT=root密码 \
//	  go test -tags manual -run TestManualServiceDoctor ./internal/app/ -v -timeout 5m
//
// 验证点：L1~L3 走 SSH、L4 本地拨测、L5 鉴权出口 IP 一致、L8 如实报告分流未启用。
func TestManualServiceDoctor(t *testing.T) {
	host := os.Getenv("DUKOU_VERIFY_HOST")
	pass := os.Getenv("DUKOU_VERIFY_PASS")
	root := os.Getenv("DUKOU_VERIFY_ROOT")
	if host == "" || pass == "" || root == "" {
		t.Skip("未设置 DUKOU_VERIFY_HOST / DUKOU_VERIFY_PASS / DUKOU_VERIFY_ROOT")
	}

	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Startup(context.Background())
	defer s.Shutdown()

	srv, err := s.AddServer(store.ServerInput{
		Name:          "体检验证线路",
		Host:          host,
		SSHUser:       "root",
		AuthType:      "password",
		SSHPort:       22,
		SSHPassword:   root,
		ProxyPassword: pass,
		ServerPort:    8443,
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	// 复用真实配置目录里已下载好的 gost.exe（测试沙箱直连不了 GitHub）
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

	defer func() {
		_ = s.Disconnect()
	}()

	// ── 场景 1：非当前线路 → 只跑 L1~L4，L5~L8 skip ──
	rep, err := s.Doctor(srv.ID)
	if err != nil {
		t.Fatalf("Doctor(非当前): %v", err)
	}
	for _, l := range rep.Layers {
		t.Logf("[非当前] %s %s -> %s: %s", l.ID, l.Name, l.Status, l.Summary)
	}
	if rep.Current {
		t.Error("未连接时 Current 应为 false")
	}
	statusByID := map[string]string{}
	for _, l := range rep.Layers {
		statusByID[l.ID] = l.Status
	}
	if statusByID["L1"] == "skip" && statusByID["L2"] == "skip" {
		t.Errorf("L1/L2 应通过 SSH 真实执行（除非 SSH 失败），实际 L1=%s L2=%s", statusByID["L1"], statusByID["L2"])
	}
	for _, id := range []string{"L5", "L6", "L8"} {
		if statusByID[id] != "skip" {
			t.Errorf("非当前线路 %s 应为 skip，实际 %s", id, statusByID[id])
		}
	}

	// ── 场景 2：连上后 → 完整体检 ──
	if _, err := s.Connect(srv.ID); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	rep2, err := s.Doctor(srv.ID)
	if err != nil {
		t.Fatalf("Doctor(当前): %v", err)
	}
	for _, l := range rep2.Layers {
		t.Logf("[当前] %s %s -> %s: %s", l.ID, l.Name, l.Status, l.Summary)
	}
	if !rep2.Current {
		t.Error("连接后 Doctor.Current 应为 true")
	}
	st2 := map[string]string{}
	for _, l := range rep2.Layers {
		st2[l.ID] = l.Status
	}
	if st2["L3"] != "pass" {
		t.Errorf("dukou.service 应为 active（真实部署过），实际 %s", st2["L3"])
	}
	if st2["L4"] != "pass" {
		t.Errorf("代理端口应可达，实际 %s", st2["L4"])
	}
	if st2["L5"] == "fail" {
		t.Errorf("鉴权层不应 fail: %v", rep2.Layers)
	}
	if st2["L8"] == "pass" {
		t.Error("分流未实现，L8 如实报告不应为 pass")
	}
	t.Logf("完整体检得分 %d，耗时 %dms，日志 %d 行", rep2.Score, rep2.ElapsedMs, len(rep2.Log))
	for _, line := range rep2.Log {
		t.Logf("  log: %s", line)
	}
}
