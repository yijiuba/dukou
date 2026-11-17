package store

import "fmt"

// lockStateID 是加密库里「订阅锁定状态」条目的固定 id。
// 与登录态（__account__）同为独立条目，互不覆盖。
// 存的是非机密派生状态（最后快照的 locked 值、登出时间、最近连接的服务器），
// 沿用加密库只是省一套存储，不是因为它保密。
const lockStateID = "__lockstate__"

// SaveLockState 保存订阅锁定派生状态（JSON blob，见 app/lockcheck.go 的定义）。
func (st *Store) SaveLockState(blob string) error {
	st.mu.RLock()
	ss := st.secrets
	st.mu.RUnlock()
	if ss == nil {
		return fmt.Errorf("凭据存储未就绪，无法保存锁定状态")
	}
	return ss.setSession(lockStateID, blob)
}

// LoadLockState 读取锁定派生状态。从未保存过返回 ok=false，不当作错误。
func (st *Store) LoadLockState() (string, bool, error) {
	st.mu.RLock()
	ss := st.secrets
	st.mu.RUnlock()
	if ss == nil {
		return "", false, fmt.Errorf("凭据存储未就绪")
	}
	return ss.session(lockStateID)
}

// ClearLockState 清除锁定派生状态（登录成功拿到新快照后不需要旧登出痕迹时使用）。
func (st *Store) ClearLockState() error {
	st.mu.RLock()
	ss := st.secrets
	st.mu.RUnlock()
	if ss == nil {
		return nil
	}
	return ss.clearSession(lockStateID)
}
