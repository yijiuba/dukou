package app

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/yijiuba/dukou/internal/account"
	"github.com/yijiuba/dukou/internal/store"
)

// ── 订阅锁定（契约 §十六「权限分级与锁定语义」，对接单 v1.1 D1-D4 定稿）──
//
// 口径（唯一来源：《账号服务接口契约》§十六，此处只消费不下发）：
//   - locked 由服务端算好（E4 account_snapshot），客户端零业务判定；
//   - 生效/解除：每次成功拉快照即刷新，续订后下次拉快照即解锁，无需重启；
//   - 离线：以最后成功快照为准，locked=true 照常锁（不降级放行），
//     locked=false 宽限内不锁（7 天横幅提示归「我的」页，不影响锁定判定）；
//   - 登出：24h 内保留最后快照的 locked 值（防登出绕过）；从未登录（无快照）不锁；
//   - 锁定范围：多服务器一键切换（保留 1 台 = 最近成功连接优先，无记录取
//     列表第一台可部署的）与批量体检。代理连接、单台体检永不受影响。
//
// 评估失败一律放行（fail-open）：锁的是增值便利，不是安全边界，
// 宁可漏锁也不能误伤代理可用性（契约「代理本身永不受影响」）。

const (
	logoutKeepDuration = 24 * time.Hour
	lockFetchTimeout   = 3 * time.Second // 切换是用户等待中的操作，离线时不能等满 20s 的 HTTP 超时
)

// lockMem 是 Service 内存里的锁定派生状态，与落盘的 lockStateBlob 字段一一对应。
type lockMem struct {
	// 最后一次成功快照的 locked 值；snapAt==0 表示从未拿到过快照。
	SnapLocked bool      `json:"snapLocked"`
	SnapAt     time.Time `json:"snapAt"`
	// 观察到登出的时刻（LoggedIn true→false 的转变点）。零值表示当前已登录或从未登录过。
	LogoutAt time.Time `json:"logoutAt"`
	// 最近一次成功连接的服务器（保留 1 台规则的优先项）。
	LastConnID string    `json:"lastConnId"`
	LastConnAt time.Time `json:"lastConnAt"`
}

// LockInfo 是给前端与 Go 内部共用的锁定判定结果。
type LockInfo struct {
	Locked bool `json:"locked"`
	// KeepID 锁定时唯一可切换的服务器；未锁定时为空。
	KeepID string `json:"keepId,omitempty"`
	// Source: none(从未登录/无快照) | fresh(本次在线拉取) | cached(离线/登出用缓存)
	Source string `json:"source"`
	// Offline: 本次判定是否没能拉到新快照（前端可用于「离线核验」横幅）。
	Offline bool `json:"offline"`
}

// subLockError 是 SUB:LOCKED 业务错误，前端据此弹续费引导。
type subLockError struct{ msg string }

func (e *subLockError) Error() string { return e.msg }

// loadLockMem 启动/首次使用时从加密库恢复派生状态（不联网）。
func (s *Service) loadLockMem() lockMem {
	s.muLock.Lock()
	defer s.muLock.Unlock()
	return s.lock
}

func (s *Service) saveLockMem(m lockMem) {
	s.muLock.Lock()
	s.lock = m
	s.muLock.Unlock()
	if blob, err := json.Marshal(m); err == nil {
		_ = s.store.SaveLockState(string(blob))
	}
}

// restoreLockMem 在首次评估前把落盘状态装进内存（幂等，进程内只装一次）。
func (s *Service) restoreLockMem() {
	s.muLock.Lock()
	if s.lockRestored {
		s.muLock.Unlock()
		return
	}
	s.lockRestored = true
	s.muLock.Unlock()

	blob, ok, err := s.store.LoadLockState()
	if err != nil || !ok || blob == "" {
		return
	}
	var m lockMem
	if jerr := json.Unmarshal([]byte(blob), &m); jerr == nil {
		s.muLock.Lock()
		// 双检：恢复期间别的调用可能已经写过内存态（以较新者为准，简单起见仅在内存仍空时采用）
		if s.lock.SnapAt.IsZero() && s.lock.LastConnID == "" {
			s.lock = m
		}
		s.muLock.Unlock()
	}
}

// noteConnected 连接成功后记录「最近成功连接」，供保留 1 台规则使用。
func (s *Service) noteConnected(serverID string) {
	m := s.loadLockMem()
	if m.LastConnID == serverID && time.Since(m.LastConnAt) < time.Minute {
		return
	}
	m.LastConnID = serverID
	m.LastConnAt = time.Now()
	s.saveLockMem(m)
}

// fetchSnapshot 带超时拉一次最新快照。离线时 3 秒返回错误，不阻塞切换。
func (s *Service) fetchSnapshot() (account.Account, error) {
	c, err := s.accountClient()
	if err != nil {
		return account.Account{}, err
	}
	if !c.LoggedIn() {
		return account.Account{}, errors.New("未登录")
	}
	type res struct {
		a   account.Account
		err error
	}
	ch := make(chan res, 1)
	go func() {
		a, err := c.Account()
		ch <- res{a, err}
	}()
	select {
	case r := <-ch:
		return r.a, r.err
	case <-time.After(lockFetchTimeout):
		return account.Account{}, errors.New("拉取订阅状态超时（离线？）")
	}
}

// SubscriptionLock 评估当前锁定状态（契约 §十六 16.2）。
// 登录时尽力在线拉新快照（成功即刷新缓存、立即生效/解除）；
// 拉不到（离线/超时）或已登出时按缓存规则判定。
func (s *Service) SubscriptionLock() (LockInfo, error) {
	s.restoreLockMem()
	m := s.loadLockMem()

	c, cerr := s.accountClient()
	loggedIn := cerr == nil && c.LoggedIn()

	if loggedIn {
		if a, err := s.fetchSnapshot(); err == nil {
			m2 := m
			m2.SnapLocked = a.Locked
			m2.SnapAt = time.Now()
			m2.LogoutAt = time.Time{} // 登录中，清掉历史登出痕迹
			s.saveLockMem(m2)
			if !a.Locked {
				return LockInfo{Locked: false, Source: "fresh"}, nil
			}
			keep := s.lockKeepID(m2)
			return LockInfo{Locked: true, KeepID: keep, Source: "fresh"}, nil
		}
		// 在线拉取失败：按缓存继续（离线规则），标记 offline 供横幅使用
		return s.lockFromCache(m, true), nil
	}

	// 未登录：登出 24h 内保留最后快照的 locked 值；超过或从未登录 → 不锁。
	if !m.SnapAt.IsZero() && !m.LogoutAt.IsZero() && time.Since(m.LogoutAt) < logoutKeepDuration {
		return s.lockFromCache(m, false), nil
	}
	return LockInfo{Locked: false, Source: "none"}, nil
}

// lockFromCache 按最后快照判定（离线/登出保留期共用）。
func (s *Service) lockFromCache(m lockMem, offline bool) LockInfo {
	if m.SnapAt.IsZero() {
		return LockInfo{Locked: false, Source: "none", Offline: offline}
	}
	if !m.SnapLocked {
		return LockInfo{Locked: false, Source: "cached", Offline: offline}
	}
	return LockInfo{Locked: true, KeepID: s.lockKeepID(m), Source: "cached", Offline: offline}
}

// lockKeepID 保留 1 台规则：最近成功连接优先；无记录或该机已不在清单/
// 未部署时，退到当前使用中的，再退到第一台可部署的，最后列表第一台。
func (s *Service) lockKeepID(m lockMem) string {
	list := s.store.List()
	if len(list) == 0 {
		return ""
	}
	deployable := func(srv store.Server) bool { return srv.Status != "pending" }
	if m.LastConnID != "" {
		for _, srv := range list {
			if srv.ID == m.LastConnID && deployable(srv) {
				return srv.ID
			}
		}
	}
	for _, srv := range list {
		if srv.Status == "active" {
			return srv.ID
		}
	}
	for _, srv := range list {
		if deployable(srv) {
			return srv.ID
		}
	}
	return list[0].ID
}

// checkSwitchAllowed 锁定前置检查：锁定且目标不是保留线路时拒绝。
// 评估失败放行（fail-open）。通过时顺带确认目标存在性由调用方完成。
func (s *Service) checkSwitchAllowed(serverID string) error {
	li, err := s.SubscriptionLock()
	if err != nil {
		return nil
	}
	if !li.Locked || li.KeepID == "" || serverID == li.KeepID {
		return nil
	}
	return &subLockError{msg: "订阅已到期：仅保留 1 台服务器可切换（最近使用的线路），续费后全部恢复"}
}
