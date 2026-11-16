package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 登录态必须能跨进程保存与清除，且明文绝不落盘。
func TestSessionRoundTripAndNoPlaintext(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	const secret = `{"accessToken":"at-secret-value","refreshToken":"rt-secret-value","email":"a@b.com"}`
	if err := st.SaveSession(secret); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	// 重新打开模拟重启应用
	st2, err := Open(dir)
	if err != nil {
		t.Fatalf("重新 Open: %v", err)
	}
	blob, ok, err := st2.LoadSession()
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if !ok || blob != secret {
		t.Fatalf("登录态没有正确恢复: ok=%v blob=%q", ok, blob)
	}

	// 任何落盘文件里都不应出现 token 明文
	for _, name := range []string{"servers.json", "secrets.json", "session.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue // 文件不存在是允许的
		}
		if strings.Contains(string(raw), "at-secret-value") || strings.Contains(string(raw), "rt-secret-value") {
			t.Fatalf("%s 里出现了 token 明文", name)
		}
	}

	// 清除后重启不应再读到
	if err := st2.ClearSession(); err != nil {
		t.Fatalf("ClearSession: %v", err)
	}
	st3, err := Open(dir)
	if err != nil {
		t.Fatalf("再次 Open: %v", err)
	}
	if _, ok, _ := st3.LoadSession(); ok {
		t.Fatal("清除后仍能读到登录态")
	}

	// 登录态与服务器凭据互不影响
	srv, err := st3.Add(ServerInput{Name: "东京", Host: "1.2.3.4", SSHPassword: "pw"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := st3.SaveSession(secret); err != nil {
		t.Fatalf("再 SaveSession: %v", err)
	}
	if _, _, err := st3.SSHCredentials(srv.ID); err != nil {
		t.Fatalf("保存登录态不应影响服务器凭据: %v", err)
	}
}
