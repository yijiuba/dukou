package app

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestProxyStatusDefaultDisconnected(t *testing.T) {
	s := newTestService(t)
	st, err := s.ProxyStatus()
	if err != nil {
		t.Fatalf("ProxyStatus: %v", err)
	}
	if st.Connected || st.Residue {
		t.Fatalf("默认应为断开态: %+v", st)
	}
}

func TestStatusPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	if err := s.saveStatus(Status{Connected: true, ServerID: "id-1", ServerName: "东京", LocalPort: 7890, EgressIP: "5.6.7.8", Since: "2026-11-16T12:00:00Z"}); err != nil {
		t.Fatalf("saveStatus: %v", err)
	}
	// 模拟重启
	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := s2.ProxyStatus()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Connected || st.ServerName != "东京" || st.EgressIP != "5.6.7.8" {
		t.Fatalf("重启后状态应还原: %+v", st)
	}
}

func TestStatusCorruptFile(t *testing.T) {
	dir := t.TempDir()
	st, _ := New(dir)
	_ = st.saveStatus(Status{Connected: true})
	if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte("bogus"), 0o600); err != nil {
		t.Fatal(err)
	}
	s2, _ := New(dir)
	if _, err := s2.ProxyStatus(); err == nil {
		t.Fatal("损坏的状态文件应报错而不是返回假数据")
	}
}

func TestConfigDir(t *testing.T) {
	s := newTestService(t)
	if s.ConfigDir() == "" {
		t.Fatal("ConfigDir 不应为空")
	}
}
