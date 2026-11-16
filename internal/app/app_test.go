package app

import (
	"context"
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

// 说明：v0.2 M2 起连接状态不再落盘到 status.json，
// 而是由 internal/proxy 现场判断（进程是否还在、系统代理是否还指向本工具）。
// 原来的「重启后还原状态」测试因此不再适用——它验证的是一个已经删掉的机制。

func TestConnectRequiresKnownServer(t *testing.T) {
	s := newTestService(t)
	s.Startup(context.Background())

	if _, err := s.Connect(""); err == nil {
		t.Fatal("空 serverID 应报错")
	}
	if _, err := s.Connect("nonexistent-id"); err == nil {
		t.Fatal("不存在的服务器应报错，而不是静默连到空地址")
	}
}

func TestConnectWithoutContext(t *testing.T) {
	s := newTestService(t)
	// 未 Startup 时 ctx 为 nil，应当明确拒绝，而不是让 proxy 拿空上下文去启动进程
	if _, err := s.Connect("any-id"); err == nil {
		t.Fatal("应用未就绪时应报错")
	}
}

func TestConfigDir(t *testing.T) {
	s := newTestService(t)
	if s.ConfigDir() == "" {
		t.Fatal("ConfigDir 不应为空")
	}
}
