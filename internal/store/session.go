package store

import "fmt"

// accountSessionID 是加密库里「账号登录态」条目的固定 id。
// 前缀不可能与服务器 uuid 冲突，两者不会互相覆盖。
const accountSessionID = "__account__"

// SaveSession 保存账号登录态（access/refresh token 的 JSON），加密后落盘。
// token 属于「泄漏即账号失守」级别的机密，因此与 SSH 密码同一套加密库，
// 绝不写进明文配置文件。
func (st *Store) SaveSession(blob string) error {
	st.mu.RLock()
	ss := st.secrets
	st.mu.RUnlock()
	if ss == nil {
		return fmt.Errorf("凭据存储未就绪，无法保存登录状态")
	}
	return ss.setSession(accountSessionID, blob)
}

// LoadSession 读取登录态。未登录过返回 ok=false，不当作错误。
func (st *Store) LoadSession() (string, bool, error) {
	st.mu.RLock()
	ss := st.secrets
	st.mu.RUnlock()
	if ss == nil {
		return "", false, fmt.Errorf("凭据存储未就绪")
	}
	return ss.session(accountSessionID)
}

// ClearSession 清除登录态（退出登录时调用）。
func (st *Store) ClearSession() error {
	st.mu.RLock()
	ss := st.secrets
	st.mu.RUnlock()
	if ss == nil {
		return nil
	}
	return ss.clearSession(accountSessionID)
}

// setSession 加密并保存登录态。
func (ss *secretStore) setSession(id, blob string) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	enc, err := ss.encrypt(blob)
	if err != nil {
		return fmt.Errorf("加密登录状态失败：%v", err)
	}
	entry := ss.entries[id]
	entry.Session = enc
	ss.entries[id] = entry
	return ss.saveLocked()
}

// session 读取并解密登录态。
func (ss *secretStore) session(id string) (string, bool, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	entry, ok := ss.entries[id]
	if !ok || entry.Session == "" {
		return "", false, nil
	}
	plain, err := ss.decrypt(entry.Session)
	if err != nil {
		return "", true, fmt.Errorf("登录状态解密失败（可能系统重装导致机器码变化），请重新登录：%v", err)
	}
	return plain, true, nil
}

// clearSession 删除登录态条目。条目里若还有别的机密则保留条目本身。
func (ss *secretStore) clearSession(id string) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	entry, ok := ss.entries[id]
	if !ok {
		return nil
	}
	entry.Session = ""
	if entry.Password == "" && entry.Key == "" && entry.Proxy == "" {
		delete(ss.entries, id)
	} else {
		ss.entries[id] = entry
	}
	return ss.saveLocked()
}
