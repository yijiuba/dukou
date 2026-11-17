package app

import (
	"testing"

	"github.com/yijiuba/dukou/internal/proxy"
)

// 启动对账：现场判定没在连接时，清单里残留的 active 要收回 standby，
// 否则首页「未连接」与服务器列表「使用中」互相矛盾（真机踩过）。
func TestProxyStatusDemotesStaleActive(t *testing.T) {
	s, servers, fake := switchFixture(t)

	// 留下一台 active 的服务器（模拟上次会话的残留标记）
	if err := s.store.SetStatus(servers[0].ID, "active"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	// 现场：gost 已死（未连接），但系统代理设置还残留着
	fake.current = proxy.Status{Connected: false, Residue: true}

	st, err := s.ProxyStatus()
	if err != nil {
		t.Fatalf("ProxyStatus: %v", err)
	}
	if st.Connected {
		t.Fatal("现场应是未连接")
	}
	got, _ := s.store.Get(servers[0].ID)
	if got.Status == "active" {
		t.Error("未连接时清单仍显示「使用中」，首页与清单矛盾")
	}
	if got.Status != "standby" {
		t.Errorf("残留 active 应收口为 standby，实际 %q", got.Status)
	}

	// 对照：连接正常时不许动状态
	if err := s.store.SetStatus(servers[0].ID, "active"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	fake.current = proxy.Status{Connected: true, ServerID: servers[0].ID}
	if _, err := s.ProxyStatus(); err != nil {
		t.Fatalf("ProxyStatus(connected): %v", err)
	}
	got, _ = s.store.Get(servers[0].ID)
	if got.Status != "active" {
		t.Errorf("连接正常时 active 被误收口为 %q", got.Status)
	}
}

// 一键清理同样是断开语义：清完当场收口，不等下一次状态查询。
func TestCleanupResidueDemotesActive(t *testing.T) {
	s, servers, fake := switchFixture(t)

	if err := s.store.SetStatus(servers[0].ID, "active"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	fake.current = proxy.Status{Connected: false, Residue: true}

	if _, err := s.CleanupResidue(); err != nil {
		t.Fatalf("CleanupResidue: %v", err)
	}
	got, _ := s.store.Get(servers[0].ID)
	if got.Status == "active" {
		t.Error("清理后清单仍显示「使用中」")
	}
}
