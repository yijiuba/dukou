package app

import (
	"encoding/json"
	"time"

	"github.com/yijiuba/dukou/internal/account"
)

// accountClient 返回账号服务客户端（懒加载单例）。
//
// token 从加密库恢复：App 重启后登录态不丢，用户不需要每天重新登录。
// 启动对账：access token 已过期时立刻主动续期；refresh token 是轮换制的
// 一次性凭证——续期成功后经钩子把新 token 落盘（见 client.SetRefreshHook）。
// 若服务端明确告知会话已作废（invalid_grant），清掉本地登录态转登录页，
// 绝不让 UI 停在「已登录但什么都加载不出来」的僵尸态。
func (s *Service) accountClient() (*account.Client, error) {
	s.muAccount.Lock()
	defer s.muAccount.Unlock()
	if s.acct != nil {
		return s.acct, nil
	}
	c := account.New(account.Config{})
	// 轮换出的新 refresh token 必须落盘，否则重启即 401（2026-11-17 真机踩坑）
	c.SetRefreshHook(func(ns account.Session) {
		_ = s.saveSession(ns)
	})
	blob, ok, err := s.store.LoadSession()
	if err != nil {
		// 解密失败不能静默当作未登录——要显式告诉用户重新登录
		return nil, err
	}
	if ok {
		var sess account.Session
		if jerr := json.Unmarshal([]byte(blob), &sess); jerr == nil && sess.AccessToken != "" {
			c.SetSession(sess)
			if expired(sess.ExpiresAt) {
				if rerr := c.RefreshSession(); rerr != nil && account.IsAuthError(rerr) {
					// 会话已被服务端作废：清本地、换干净客户端，UI 如实显示未登录
					_ = s.store.ClearSession()
					c = account.New(account.Config{})
				}
			}
		}
	}
	s.acct = c
	return c, nil
}

// expired 判断会话过期时间是否已过（空值视为未过期，由服务端 401 兜底）。
func expired(iso string) bool {
	if iso == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, iso)
	return err == nil && time.Now().After(t)
}

// saveSession 把登录态加密落盘（与 SSH 凭据同一套 AES-GCM 库）。
func (s *Service) saveSession(sess account.Session) error {
	blob, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	return s.store.SaveSession(string(blob))
}

// SessionInfo 是界面启动时判断"要不要显示登录"的依据。
type SessionInfo struct {
	LoggedIn bool   `json:"loggedIn"`
	Email    string `json:"email"`
}

// SessionInfo 返回当前登录态（不请求网络）。
func (s *Service) SessionInfo() (SessionInfo, error) {
	c, err := s.accountClient()
	if err != nil {
		return SessionInfo{}, err
	}
	return SessionInfo{LoggedIn: c.LoggedIn(), Email: c.LoggedInEmail()}, nil
}

// LoginRequestCode 发送邮箱验证码（契约 E1）。
// 返回的 VerificationID 由前端保存，提交验证码时原样带回。
func (s *Service) LoginRequestCode(email string) (account.CodeSent, error) {
	c, err := s.accountClient()
	if err != nil {
		return account.CodeSent{}, err
	}
	return c.RequestCode(email)
}

// LoginResult 登录成功的返回：订阅状态一起带回来，前端少一次往返。
type LoginResult struct {
	Email   string          `json:"email"`
	Account account.Account `json:"account"`
}

// LoginVerify 校验验证码并完成登录/注册（契约 E2，注册登录合一）。
func (s *Service) LoginVerify(email, verificationID string, isExisting bool, code string) (LoginResult, error) {
	c, err := s.accountClient()
	if err != nil {
		return LoginResult{}, err
	}
	sess, err := c.VerifyCode(email, verificationID, isExisting, code)
	if err != nil {
		return LoginResult{}, err
	}
	// 落盘失败不能让登录失败（本次会话仍然可用），但要如实告知
	saveErr := s.saveSession(sess)

	out := LoginResult{Email: sess.Email}
	if a, aerr := c.Account(); aerr == nil {
		out.Account = a
	}
	if saveErr != nil {
		return out, errSessionNotSaved{saveErr}
	}
	return out, nil
}

// errSessionNotSaved 表示登录成功但本地保存失败。
// 独立类型是为了让界面可以提示"重启后需要重新登录"，而不是报"登录失败"。
type errSessionNotSaved struct{ err error }

func (e errSessionNotSaved) Error() string {
	return "已登录，但登录状态无法保存到本机（" + e.err.Error() + "），重启应用后需要重新登录"
}

// Logout 退出登录：清本地 token + 尽力吊销服务端会话。
func (s *Service) Logout() error {
	c, err := s.accountClient()
	if err != nil {
		return err
	}
	_ = c.Logout()
	s.stopHeartbeat()
	return s.store.ClearSession()
}

// Account 拉取订阅状态（契约 E4）。
func (s *Service) Account() (account.Account, error) {
	c, err := s.accountClient()
	if err != nil {
		return account.Account{}, err
	}
	return c.Account()
}

// Plans 套餐列表（契约 E6，公开端点，不要求登录）。
func (s *Service) Plans() ([]account.Plan, error) {
	c, err := s.accountClient()
	if err != nil {
		return nil, err
	}
	return c.Plans()
}

// ClaimTrial 领取 3 天试用（契约 E5，幂等）。
func (s *Service) ClaimTrial() (account.TrialResult, error) {
	c, err := s.accountClient()
	if err != nil {
		return account.TrialResult{}, err
	}
	return c.ClaimTrial()
}

// Orders 我的订单（契约 E7）。
func (s *Service) Orders() ([]account.Order, error) {
	c, err := s.accountClient()
	if err != nil {
		return nil, err
	}
	return c.Orders()
}

// Referral 推广信息（契约 E8）。
func (s *Service) Referral() (account.Referral, error) {
	c, err := s.accountClient()
	if err != nil {
		return account.Referral{}, err
	}
	return c.Referral()
}

// ── 增值服务（契约 v1.1 E10-E12）──────────────────────────────────

// Products 商品目录（E10，公开）。未登录也可拉取，用于展示商品卡。
func (s *Service) Products() ([]account.Product, error) {
	c, err := s.accountClient()
	if err != nil {
		return nil, err
	}
	return c.Products()
}

// Entitlements 我的权益（E11）。未登录时返回空列表而非报错——
// 「我持有的服务」条在未登录态只是不显示。
func (s *Service) Entitlements() ([]account.Entitlement, error) {
	c, err := s.accountClient()
	if err != nil {
		return nil, err
	}
	if !c.LoggedIn() {
		return []account.Entitlement{}, nil
	}
	return c.Entitlements()
}

// ServiceOrders 我的服务订单（E12b）。
func (s *Service) ServiceOrders() ([]account.ServiceOrder, error) {
	c, err := s.accountClient()
	if err != nil {
		return nil, err
	}
	if !c.LoggedIn() {
		return []account.ServiceOrder{}, nil
	}
	return c.ServiceOrders()
}

// CreateServiceOrder 提交增值服务订单（E12）。一期人工收款，返回 pending 订单。
func (s *Service) CreateServiceOrder(productID, note, scheduledAt string) (account.ServiceOrder, error) {
	c, err := s.accountClient()
	if err != nil {
		return account.ServiceOrder{}, err
	}
	if !c.LoggedIn() {
		return account.ServiceOrder{}, &account.APIError{Code: "AUTH:SESSION", Message: "请先登录再预约服务"}
	}
	return c.CreateServiceOrder(productID, note, scheduledAt)
}

// ── 心跳（契约 E9）────────────────────────────────────────────────
//
// 口径：App 打开且代理已连接 → 每 5 分钟上报 5 分钟；未连接不发（省电省流量）。
// 上报失败静默丢弃：心跳是后台行为，绝不能影响用户正在用的功能。
// 服务端按自然日封顶 600 分钟，客户端不需要自己算。

const heartbeatInterval = 5 * time.Minute

func (s *Service) startHeartbeat() {
	s.hbMu.Lock()
	if s.hbStop != nil {
		s.hbMu.Unlock()
		return
	}
	stop := make(chan struct{})
	s.hbStop = stop
	s.hbMu.Unlock()

	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				c, err := s.accountClient()
				if err != nil || !c.LoggedIn() {
					continue
				}
				st, err := s.ProxyStatus()
				if err != nil || !st.Connected {
					continue
				}
				_, _ = c.Heartbeat(int(heartbeatInterval / time.Minute))
			}
		}
	}()
}

func (s *Service) stopHeartbeat() {
	s.hbMu.Lock()
	defer s.hbMu.Unlock()
	if s.hbStop != nil {
		close(s.hbStop)
		s.hbStop = nil
	}
}
