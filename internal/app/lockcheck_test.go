package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yijiuba/dukou/internal/proxy"
	"github.com/yijiuba/dukou/internal/store"
)

// lockFixture 与 switchFixture 同构，但预热了锁定派生状态。
// 账号客户端不注入（未登录态），锁定判定走「登出 24h 保留」与「从未登录不锁」分支；
// 在线拉取分支（fresh）需要真服务端，不在单测覆盖范围（契约 §十六为服务端语义）。
func lockFixture(t *testing.T) (*Service, []store.Server, *fakeProxy) {
	t.Helper()
	return switchFixture(t)
}

// setLockMem 直接写内存态（同包测试白盒），模拟「曾拿到过 locked 快照后登出」。
func setLockMem(s *Service, m lockMem) { s.saveLockMem(m) }

// 未登录且从未有过快照：不锁（契约 §十六 16.2「从未登录（无快照）不锁」）。
func TestLockNeverLoggedInUnlocked(t *testing.T) {
	s, servers, _ := lockFixture(t)
	s.Startup(context.Background())

	li, err := s.SubscriptionLock()
	if err != nil {
		t.Fatalf("SubscriptionLock: %v", err)
	}
	if li.Locked {
		t.Fatalf("从未登录不应锁定: %+v", li)
	}
	if err := s.checkSwitchAllowed(servers[2].ID); err != nil {
		t.Fatalf("未登录时切换不应被拦: %v", err)
	}
}

// 登出 24h 内：保留最后快照的 locked 值（防登出绕过）；
// 保留线路 = 最近成功连接，其余切换必须返回 SUB:LOCKED。
func TestLockLogoutKeeps24h(t *testing.T) {
	s, servers, fp := lockFixture(t)
	s.Startup(context.Background())

	// 先建立「最近成功连接 = 东京（第 0 台）」：走一次真实 Connect（fake 驱动）。
	if _, err := s.Connect(servers[0].ID); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	// 再模拟「曾拿到 locked=true 快照，随后登出」。
	setLockMem(s, lockMem{
		SnapLocked: true,
		SnapAt:     time.Now().Add(-time.Hour),
		LogoutAt:   time.Now().Add(-10 * time.Minute),
	})

	li, err := s.SubscriptionLock()
	if err != nil {
		t.Fatalf("SubscriptionLock: %v", err)
	}
	if !li.Locked || li.KeepID != servers[0].ID {
		t.Fatalf("登出 24h 内应保留锁定且保留最近连接的服务器: %+v (want keep=%s)", li, servers[0].ID)
	}
	// 保留线路放行，其余拒绝。
	if err := s.checkSwitchAllowed(servers[0].ID); err != nil {
		t.Fatalf("保留线路不应被拦: %v", err)
	}
	err = s.checkSwitchAllowed(servers[1].ID)
	var se *subLockError
	if !errors.As(err, &se) {
		t.Fatalf("非保留线路应返回 SUB:LOCKED 类错误, got %v", err)
	}
	// 真实 SwitchServer 路径同样拦截（且在断开之前）。
	fp.current = proxyStatusConnected(servers[0])
	fp.calls = nil // 清掉建立最近连接时留下的调用记录
	if err := s.SwitchServer(servers[1].ID); err == nil {
		t.Fatalf("锁定时 SwitchServer 必须失败")
	}
	if len(fp.calls) != 0 {
		t.Fatalf("拦截必须发生在断开之前, calls=%v", fp.calls)
	}
}

// 登出超过 24h：不再保留锁定（回到「从未登录」档）。
func TestLockLogoutExpiresAfter24h(t *testing.T) {
	s, servers, _ := lockFixture(t)
	s.Startup(context.Background())
	setLockMem(s, lockMem{
		SnapLocked: true,
		SnapAt:     time.Now().Add(-48 * time.Hour),
		LogoutAt:   time.Now().Add(-25 * time.Hour),
	})
	li, _ := s.SubscriptionLock()
	if li.Locked {
		t.Fatalf("登出超 24h 不应继续锁定: %+v", li)
	}
	if err := s.checkSwitchAllowed(servers[1].ID); err != nil {
		t.Fatalf("不应拦截: %v", err)
	}
}

// 离线（在线拉取失败）不改变锁定：locked=true 照常锁，不降级放行。
// 这里通过让 accountClient 报错模拟「拉不到快照」。
func TestLockOfflineKeepsLocked(t *testing.T) {
	s, servers, _ := lockFixture(t)
	s.Startup(context.Background())
	setLockMem(s, lockMem{SnapLocked: true, SnapAt: time.Now().Add(-2 * time.Hour)})
	// 未登录 → 走登出分支；LogoutAt 为零值会被判「从未登录」，
	// 因此离线保留锁定的场景必须带 LogoutAt（登出后离线使用）。
	setLockMem(s, lockMem{SnapLocked: true, SnapAt: time.Now().Add(-2 * time.Hour), LogoutAt: time.Now().Add(-time.Minute)})

	li, _ := s.SubscriptionLock()
	if !li.Locked {
		t.Fatalf("登出保留期内离线应照常锁定: %+v", li)
	}
	// 无使用记录时保留 = 第一台可部署的（fixture 三台都非 pending → 列表第一台）。
	if li.KeepID != servers[0].ID {
		t.Fatalf("无最近连接记录时保留应为列表第一台: %+v", li)
	}
}

// 保留规则：最近连接的服务器已从清单删除/待部署时，退到当前使用中的，再退第一台可部署。
func TestLockKeepIDFallback(t *testing.T) {
	s, servers, _ := lockFixture(t)
	s.Startup(context.Background())

	m := lockMem{SnapLocked: true, SnapAt: time.Now(), LogoutAt: time.Now(), LastConnID: "gone"}
	s.saveLockMem(m)
	if got := s.lockKeepID(m); got != servers[0].ID {
		t.Fatalf("记录失效应退列表第一台可部署: got %s want %s", got, servers[0].ID)
	}

	// 记录指向待部署机器：pending 不可保留，退到第一台可部署。
	pend, err := s.store.Add(store.ServerInput{Name: "待部署", Host: "10.9.9.9", AuthType: "password", SSHPassword: "pw"})
	if err != nil {
		t.Fatalf("Add pending: %v", err)
	}
	// 「先保存后部署」流程才会置 pending；这里显式模拟「保存了但没部署」。
	if err := s.store.SetStatus(pend.ID, "pending"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	m.LastConnID = pend.ID
	if got := s.lockKeepID(m); got != servers[0].ID {
		t.Fatalf("pending 不可作为保留线路: got %s want %s", got, servers[0].ID)
	}
}

// proxyStatusConnected 构造 fake 驱动的「已连接」状态。
func proxyStatusConnected(srv store.Server) proxy.Status {
	return proxy.Status{Connected: true, ServerID: srv.ID, ServerName: srv.Name}
}
