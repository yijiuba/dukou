// Package account 是账号服务的客户端实现（契约 `账号服务接口契约-v1.0.md`）。
//
// 两条通道，都由同一个 HTTPS 数据面承载：
//   - 认证：`.cloud/auth/v1/verification|verify|signin|signup|token|user/signout`
//   - 业务：`.cloud/database/rest/*`（PostgREST，读表与 RPC）
//
// 业务判定（locked、推广等级、每日 600 分钟封顶）全部在服务端的
// SECURITY DEFINER 函数里完成，客户端不做任何业务计算——这是
// 「防本地篡改」与「将来换后端不动客户端」两条要求的落点。
package account

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// 数据面默认配置。publishableKey 只标识应用、本身不含权限（权限来自用户 token 与 RLS），
// 因此可以随客户端分发；环境变量可覆盖，便于指向 dev 环境。
const (
	DefaultBaseURL        = "https://wbapp-a0pris8liqm6ifdq5zzpyy.app.workbuddy.host"
	DefaultPublishableKey = "wbpk_a0pRIs8lIQm6ifdq5zzpyY_MUYBS9YzVbd7PnPi75vPErxGDLVgT9SS"

	envBaseURL = "DUKOU_API_BASE"
	envAPIKey  = "DUKOU_API_KEY"
)

// Config 是客户端连接参数。零值合法（用默认数据面）。
type Config struct {
	BaseURL        string
	PublishableKey string
}

func (c Config) withDefaults() Config {
	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
	}
	if c.PublishableKey == "" {
		c.PublishableKey = DefaultPublishableKey
	}
	if v := os.Getenv(envBaseURL); v != "" {
		c.BaseURL = v
	}
	if v := os.Getenv(envAPIKey); v != "" {
		c.PublishableKey = v
	}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	return c
}

// APIError 是服务端返回的业务错误。Code 沿用契约前缀码（AUTH:/NET:/CFG:/SYS:）。
type APIError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"-"`
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Code != "" {
		return e.Code
	}
	return fmt.Sprintf("账号服务返回 HTTP %d", e.HTTPStatus)
}

// IsAuthError 判断是否是「会话失效」类错误（客户端应清 token 转登录页）。
func IsAuthError(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code == "AUTH:SESSION" || ae.HTTPStatus == http.StatusUnauthorized
	}
	return false
}

// ── 数据结构（字段名与契约 JSON 一一对应）─────────────────────────────

// Account 对应契约 E4。locked 由服务端算好，客户端只消费。
type Account struct {
	TrialAvailable bool   `json:"trialAvailable"`
	TrialEndsAt    string `json:"trialEndsAt"`
	PlanID         string `json:"planId"`
	ExpiresAt      string `json:"expiresAt"`
	Locked         bool   `json:"locked"`
}

// Plan 对应契约 E6。
type Plan struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Days        int    `json:"days"`
	PriceCents  int    `json:"priceCents"`
	RenewalHint string `json:"renewalHint"`
	Order       int    `json:"order"`
}

// Order 对应契约 E7。
type Order struct {
	ID         string `json:"id"`
	PlanID     string `json:"planId"`
	PlanName   string `json:"planName"`
	PriceCents int    `json:"priceCents"`
	Status     string `json:"status"`
	Channel    string `json:"channel"`
	CreatedAt  string `json:"createdAt"`
	PaidAt     string `json:"paidAt"`
	StartsAt   string `json:"startsAt"`
	EndsAt     string `json:"endsAt"`
}

// Referral 对应契约 E8。
type Referral struct {
	Code          string  `json:"code"`
	Level         int     `json:"level"`
	LevelName     string  `json:"levelName"`
	CommissionPct int     `json:"commissionPct"`
	OnlineHours   float64 `json:"onlineHours"`
	Orders        int     `json:"orders"`
	EarnedCents   int     `json:"earnedCents"`
	BalanceCents  int     `json:"balanceCents"`
	NextLevelHint string  `json:"nextLevelHint"`
}

// Session 是本地保存的登录态。token 只进加密库，绝不明文落盘。
type Session struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	Email        string `json:"email"`
	ExpiresAt    string `json:"expiresAt"`
}

// CodeSent 是 E1 的结果，前端需要在两次请求之间保存它。
type CodeSent struct {
	VerificationID string `json:"verificationId"`
	IsExistingUser bool   `json:"isExistingUser"`
	RetryAfterSec  int    `json:"retryAfterSec"`
}

// TrialResult 对应 E5 的实现形态：BaaS RPC 恒返回 200，
// 用 Status 表达 `created` / `already`（见契约附录）。
type TrialResult struct {
	Status  string  `json:"status"`
	Account Account `json:"account"`
}

// HeartbeatResult 对应 E9。
type HeartbeatResult struct {
	OnlineHours    float64 `json:"onlineHours"`
	TodayCappedMin int     `json:"todayCappedMin"`
	ServerTime     string  `json:"serverTime"`
}

// ── 客户端 ──────────────────────────────────────────────────────────

// Client 并发安全。同一实例内持有登录态；跨进程持久化由 store 负责。
type Client struct {
	cfg Config
	hc  *http.Client

	mu      sync.RWMutex
	access  string
	refresh string
	email   string
}

// New 创建客户端。config 为零值时使用默认数据面。
func New(cfg Config) *Client {
	return &Client{
		cfg: cfg.withDefaults(),
		hc:  &http.Client{Timeout: 20 * time.Second},
	}
}

// SetSession 注入已持久化的登录态（App 启动时调用）。
func (c *Client) SetSession(s Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.access, c.refresh, c.email = s.AccessToken, s.RefreshToken, s.Email
}

// Session 返回当前登录态（供落盘）。
func (c *Client) Session() Session {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Session{AccessToken: c.access, RefreshToken: c.refresh, Email: c.email}
}

// LoggedIn 是否持有会话。
func (c *Client) LoggedIn() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.access != ""
}

// LoggedInEmail 返回登录邮箱（可能为空）。
func (c *Client) LoggedInEmail() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.email
}

// RequestCode 发送邮箱验证码（E1）。
func (c *Client) RequestCode(email string) (CodeSent, error) {
	email = strings.TrimSpace(email)
	if !strings.Contains(email, "@") || strings.HasPrefix(email, "@") {
		return CodeSent{}, &APIError{Code: "CFG:PARAM", Message: "请输入正确的邮箱地址"}
	}
	var raw struct {
		VerificationID string `json:"verification_id"`
		IsUser         bool   `json:"is_user"`
		RetryAfter     int    `json:"retry_after"`
	}
	err := c.do(http.MethodPost, "/.cloud/auth/v1/verification",
		map[string]any{"email": email}, &raw, false)
	if err != nil {
		return CodeSent{}, err
	}
	if raw.VerificationID == "" {
		return CodeSent{}, &APIError{Code: "SYS:SERVER", Message: "服务端没有返回验证码标识，请稍后重试"}
	}
	retry := raw.RetryAfter
	if retry <= 0 {
		retry = 60
	}
	return CodeSent{VerificationID: raw.VerificationID, IsExistingUser: raw.IsUser, RetryAfterSec: retry}, nil
}

// VerifyCode 校验验证码并完成登录/注册（E2，注册登录合一）。
func (c *Client) VerifyCode(email, verificationID string, isExisting bool, code string) (Session, error) {
	email = strings.TrimSpace(email)
	code = strings.TrimSpace(code)
	if verificationID == "" || code == "" {
		return Session{}, &APIError{Code: "CFG:PARAM", Message: "请先获取验证码再提交"}
	}

	var verified struct {
		VerificationToken string `json:"verification_token"`
	}
	err := c.do(http.MethodPost, "/.cloud/auth/v1/verification/verify", map[string]any{
		"email":            email,
		"verification_id":  verificationID,
		"is_existing_user": isExisting,
		"token":            code,
	}, &verified, false)
	if err != nil {
		return Session{}, err
	}
	if verified.VerificationToken == "" {
		return Session{}, &APIError{Code: "AUTH:CODE", Message: "验证码无效或已过期，请重新获取"}
	}

	path := "/.cloud/auth/v1/signin"
	if !isExisting {
		path = "/.cloud/auth/v1/signup"
	}
	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	err = c.do(http.MethodPost, path, map[string]any{
		"email":              email,
		"verification_token": verified.VerificationToken,
	}, &raw, false)
	if err != nil {
		return Session{}, err
	}
	if raw.AccessToken == "" {
		return Session{}, &APIError{Code: "AUTH:SESSION", Message: "登录成功但未拿到会话，请重试"}
	}

	expires := ""
	if raw.ExpiresIn > 0 {
		expires = time.Now().Add(time.Duration(raw.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	s := Session{AccessToken: raw.AccessToken, RefreshToken: raw.RefreshToken, Email: email, ExpiresAt: expires}
	c.SetSession(s)
	return s, nil
}

// Logout 清本地会话，并尽力通知服务端吊销（通知失败不阻塞退出）。
func (c *Client) Logout() error {
	c.mu.Lock()
	had := c.access != ""
	c.access, c.refresh, c.email = "", "", ""
	c.mu.Unlock()
	if !had {
		return nil
	}
	// 服务端吊销失败不影响用户"已退出"的事实
	_ = c.do(http.MethodPost, "/.cloud/auth/v1/user/signout", map[string]any{}, nil, true)
	return nil
}

// Account 拉取订阅状态（E4）。
func (c *Client) Account() (Account, error) {
	var a Account
	err := c.rpc("account_snapshot", map[string]any{}, &a)
	return a, err
}

// ClaimTrial 领取 3 天试用（E5，幂等）。
func (c *Client) ClaimTrial() (TrialResult, error) {
	var r TrialResult
	err := c.rpc("claim_trial", map[string]any{}, &r)
	return r, err
}

// Plans 套餐列表（E6，公开端点，无需登录）。
func (c *Client) Plans() ([]Plan, error) {
	q := url.Values{}
	q.Set("select", "id,name,days,priceCents:price_cents,renewalHint:renewal_hint,sortOrder:sort_order")
	q.Set("order", "sort_order")
	q.Set("active", "eq.true")
	var out []Plan
	err := c.do(http.MethodGet, "/.cloud/database/rest/plans?"+q.Encode(), nil, &out, false)
	return out, err
}

// Orders 我的订单（E7，RLS 自动限本人）。
func (c *Client) Orders() ([]Order, error) {
	q := url.Values{}
	q.Set("select", "id,planId:plan_id,planName:plan_name,priceCents:price_cents,status,channel,"+
		"createdAt:created_at,paidAt:paid_at,startsAt:starts_at,endsAt:ends_at")
	q.Set("order", "created_at.desc")
	var out []Order
	err := c.do(http.MethodGet, "/.cloud/database/rest/orders?"+q.Encode(), nil, &out, true)
	return out, err
}

// Referral 推广信息（E8）。
func (c *Client) Referral() (Referral, error) {
	var r Referral
	err := c.rpc("referral_snapshot", map[string]any{}, &r)
	return r, err
}

// Heartbeat 上报有效在线分钟数（E9）。失败由调用方决定是否重试。
func (c *Client) Heartbeat(activeMin int) (HeartbeatResult, error) {
	var r HeartbeatResult
	err := c.rpc("heartbeat", map[string]any{"active_min": activeMin}, &r)
	return r, err
}

// rpc 调用 PostgREST 的数据库函数端点。
func (c *Client) rpc(fn string, args map[string]any, out any) error {
	return c.do(http.MethodPost, "/.cloud/database/rest/rpc/"+fn, args, out, true)
}

// do 是所有请求的唯一出口：统一注入请求头、统一错误翻译、统一 401 续期。
func (c *Client) do(method, path string, body any, out any, auth bool) error {
	if err := c.doOnce(method, path, body, out, auth); err != nil {
		if auth && IsAuthError(err) && c.tryRefresh() {
			return c.doOnce(method, path, body, out, auth)
		}
		return err
	}
	return nil
}

func (c *Client) doOnce(method, path string, body any, out any, auth bool) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("序列化请求失败：%v", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, c.cfg.BaseURL+path, reader)
	if err != nil {
		return fmt.Errorf("构造请求失败：%v", err)
	}
	// 原生客户端不需要 Origin：平台只对浏览器做跨站校验（已实测，见契约附录）
	req.Header.Set("x-wb-webapp-access-key", c.cfg.PublishableKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		c.mu.RLock()
		token := c.access
		c.mu.RUnlock()
		if token == "" {
			return &APIError{Code: "AUTH:SESSION", Message: "请先登录"}
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return &APIError{Code: "NET:UNREACHABLE", Message: "连不上账号服务，请检查网络后重试"}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return parseError(resp.StatusCode, raw)
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return &APIError{Code: "SYS:SERVER", Message: "账号服务返回了无法解析的数据，请稍后重试"}
	}
	return nil
}

// tryRefresh 用 refresh token 换新会话。成功返回 true。
func (c *Client) tryRefresh() bool {
	c.mu.RLock()
	rt := c.refresh
	c.mu.RUnlock()
	if rt == "" {
		return false
	}
	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	err := c.doOnce(http.MethodPost, "/.cloud/auth/v1/token", map[string]any{
		"grant_type":    "refresh_token",
		"refresh_token": rt,
	}, &raw, false)
	if err != nil || raw.AccessToken == "" {
		return false
	}
	c.mu.Lock()
	c.access = raw.AccessToken
	if raw.RefreshToken != "" {
		c.refresh = raw.RefreshToken
	}
	c.mu.Unlock()
	return true
}

// parseError 把服务端的各种错误体翻译成带前缀码的 APIError。
// 平台错误体有多种形状：{code,message}、{error,error_description}、
// PostgREST 的 {code,message,details,hint}。
func parseError(status int, raw []byte) error {
	var payload struct {
		Code        string `json:"code"`
		Message     string `json:"message"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &payload)

	msg := payload.Message
	if msg == "" {
		msg = payload.Description
	}
	code := payload.Code
	if code == "" {
		code = payload.Error
	}
	if code == "" {
		code = "SYS:SERVER"
	}
	if msg == "" {
		switch status {
		case http.StatusUnauthorized:
			msg = "登录已过期，请重新登录"
		case http.StatusForbidden:
			msg = "没有权限执行这个操作"
		case http.StatusTooManyRequests:
			msg = "请求太频繁，请稍后再试"
		default:
			msg = fmt.Sprintf("账号服务暂时不可用（HTTP %d），请稍后重试", status)
		}
	}
	if status == http.StatusUnauthorized {
		code = "AUTH:SESSION"
	}
	return &APIError{Code: code, Message: msg, HTTPStatus: status}
}
