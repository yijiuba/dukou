package account

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(Config{BaseURL: srv.URL, PublishableKey: "wbpk_test"})
}

// 每个请求都必须带应用标识头，否则服务端一律拒绝。
func TestRequestsCarryAppKey(t *testing.T) {
	var missing int32
	var missingOrigin int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-wb-webapp-access-key") != "wbpk_test" {
			atomic.AddInt32(&missing, 1)
		}
		// 平台要求所有请求带白名单内 Origin（token 的 azp 绑定它），见 client.go 注释
		if r.Header.Get("Origin") == "" {
			atomic.AddInt32(&missingOrigin, 1)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"verification_id": "vid", "is_user": false})
	})
	if _, err := c.RequestCode("a@b.com"); err != nil {
		t.Fatalf("RequestCode: %v", err)
	}
	if missing != 0 {
		t.Fatalf("有 %d 个请求没带 x-wb-webapp-access-key", missing)
	}
	if missingOrigin != 0 {
		t.Fatalf("有 %d 个请求没带 Origin 头", missingOrigin)
	}
}

func TestRequestCodeRejectsBadEmail(t *testing.T) {
	c := New(Config{})
	if _, err := c.RequestCode("not-an-email"); err == nil {
		t.Fatal("非法邮箱应当直接拒绝，不发请求")
	}
}

// 登录流程：验证码 → 校验换 verification_token → 登录拿会话。
func TestVerifyCodeSignsIn(t *testing.T) {
	var gotPaths []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		switch r.URL.Path {
		case "/.cloud/auth/v1/verification/verify":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["verification_code"] != "123456" || body["verification_id"] != "vid" {
				t.Errorf("校验请求体不对: %v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"verification_token": "vt-1"})
		case "/.cloud/auth/v1/signin":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["verification_token"] != "vt-1" || body["username"] != "a@b.com" {
				t.Errorf("登录请求应带 username(=邮箱) + verification_token，实际 %v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "at-1", "refresh_token": "rt-1", "expires_in": 3600,
			})
		default:
			t.Errorf("非预期路径 %s", r.URL.Path)
		}
	})

	sess, err := c.VerifyCode("a@b.com", "vid", true, "123456")
	if err != nil {
		t.Fatalf("VerifyCode: %v", err)
	}
	if sess.AccessToken != "at-1" || sess.RefreshToken != "rt-1" || sess.Email != "a@b.com" {
		t.Fatalf("会话字段不对: %+v", sess)
	}
	if sess.ExpiresAt == "" {
		t.Fatal("expires_in 应当换算成 ExpiresAt")
	}
	if !c.LoggedIn() {
		t.Fatal("登录后 LoggedIn 应为 true")
	}
	want := []string{"/.cloud/auth/v1/verification/verify", "/.cloud/auth/v1/signin"}
	if len(gotPaths) != len(want) {
		t.Fatalf("请求次数不对: %v", gotPaths)
	}
	for i := range want {
		if gotPaths[i] != want[i] {
			t.Fatalf("第 %d 个请求路径 %s，期望 %s", i+1, gotPaths[i], want[i])
		}
	}
}

// 新用户走 signup 而不是 signin——走错会把首次注册变成"用户不存在"。
func TestVerifyCodeNewUserSignsUp(t *testing.T) {
	var signupHit bool
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.cloud/auth/v1/verification/verify":
			_ = json.NewEncoder(w).Encode(map[string]any{"verification_token": "vt-1"})
		case "/.cloud/auth/v1/signup":
			signupHit = true
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["email"] != "new@b.com" || body["verification_token"] != "vt-1" {
				t.Errorf("signup 请求应带 email + verification_token，实际 %v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at-2"})
		default:
			t.Errorf("非预期路径 %s", r.URL.Path)
		}
	})
	if _, err := c.VerifyCode("new@b.com", "vid", false, "123456"); err != nil {
		t.Fatalf("VerifyCode: %v", err)
	}
	if !signupHit {
		t.Fatal("新用户应当走 /v1/signup")
	}
}

// 401 时用 refresh token 自动续期并重试一次，用户无感。
func TestAutoRefreshOn401(t *testing.T) {
	var calls int32
	var sawNewToken bool
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.cloud/database/rest/rpc/account_snapshot":
			if atomic.AddInt32(&calls, 1) == 1 {
				if r.Header.Get("Authorization") != "Bearer old" {
					t.Errorf("首次请求应带旧 token，实际 %q", r.Header.Get("Authorization"))
				}
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":"AUTH:SESSION","message":"expired"}`))
				return
			}
			sawNewToken = r.Header.Get("Authorization") == "Bearer new"
			_, _ = w.Write([]byte(`{"trialAvailable":true,"trialEndsAt":"","planId":"","expiresAt":"","locked":false}`))
		case "/.cloud/auth/v1/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new", "refresh_token": "rt-new"})
		default:
			t.Errorf("非预期路径 %s", r.URL.Path)
		}
	})
	c.SetSession(Session{AccessToken: "old", RefreshToken: "rt"})

	a, err := c.Account()
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	if !a.TrialAvailable {
		t.Fatalf("应返回服务端给的 account: %+v", a)
	}
	if !sawNewToken {
		t.Fatal("续期后重试应使用新 token")
	}
	if c.Session().RefreshToken != "rt-new" {
		t.Fatal("refresh token 轮换后应当更新本地值")
	}
}

// 限频错误要把服务端的中文提示原样传给用户，而不是吞成"HTTP 429"。
func TestRateLimitMessagePreserved(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":"AUTH:RATE","message":"验证码发送太频繁，请 60 秒后再试"}`))
	})
	_, err := c.RequestCode("a@b.com")
	if err == nil {
		t.Fatal("限频应当报错")
	}
	if !strings.Contains(err.Error(), "60 秒") {
		t.Fatalf("错误信息应保留服务端原文，实际 %q", err.Error())
	}
}

// 套餐查询必须用别名把 snake_case 对齐成契约里的 camelCase。
func TestPlansQueryUsesAliases(t *testing.T) {
	var selectParam string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		selectParam = r.URL.Query().Get("select")
		_, _ = w.Write([]byte(`[{"id":"monthly","name":"月卡","days":30,"priceCents":1900,"sortOrder":2}]`))
	})
	plans, err := c.Plans()
	if err != nil {
		t.Fatalf("Plans: %v", err)
	}
	if !strings.Contains(selectParam, "priceCents:price_cents") {
		t.Fatalf("select 缺少 camelCase 别名: %s", selectParam)
	}
	if len(plans) != 1 || plans[0].PriceCents != 1900 {
		t.Fatalf("解析结果不对: %+v", plans)
	}
}

// 未登录访问需要身份的接口，应当在本地就被拦住，不发请求。
func TestOrdersRequiresLogin(t *testing.T) {
	c := New(Config{BaseURL: "http://127.0.0.1:1", PublishableKey: "wbpk_test"})
	if _, err := c.Orders(); err == nil || !IsAuthError(err) {
		t.Fatalf("未登录应返回 AUTH:SESSION，实际 %v", err)
	}
}

// 增值服务（契约 v1.1）：商品读表、权益与下单走 RPC，字段别名必须对上。
func TestProductsMapsColumns(t *testing.T) {
	var query string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.cloud/database/rest/products" {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		query = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id": "svc_course_ai", "kind": "course", "name": "AI 出海课程包",
			"summary": "s", "priceCents": 1990, "delivery": "self", "sort": 10,
			"meta": map[string]any{"chapters": []string{"a"}},
		}})
	})
	ps, err := c.Products()
	if err != nil {
		t.Fatalf("Products: %v", err)
	}
	if len(ps) != 1 || ps[0].PriceCents != 1990 || ps[0].Kind != "course" {
		t.Fatalf("商品解析错误: %+v", ps)
	}
	if !strings.Contains(query, "priceCents%3Aprice_cents") || !strings.Contains(query, "status=eq.on") {
		t.Fatalf("查询串缺少别名或上架过滤: %s", query)
	}
}

func TestCreateServiceOrderSendsRPCArgs(t *testing.T) {
	var path string
	var body map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "so_1", "productId": "svc_coach_reg", "productName": "注册陪跑服务",
			"priceCents": 3900, "status": "pending", "channel": "manual",
		})
	})
	c.SetSession(Session{AccessToken: "tok", Email: "a@b.com"})
	o, err := c.CreateServiceOrder("svc_coach_reg", "周末晚上", "")
	if err != nil {
		t.Fatalf("CreateServiceOrder: %v", err)
	}
	if path != "/.cloud/database/rest/rpc/create_service_order" {
		t.Fatalf("RPC 路径错误: %s", path)
	}
	if body["p_product_id"] != "svc_coach_reg" || body["p_note"] != "周末晚上" {
		t.Fatalf("RPC 参数错误: %+v", body)
	}
	if _, ok := body["p_scheduled_at"]; ok {
		t.Fatalf("空预约时间不应发送 p_scheduled_at: %+v", body)
	}
	if o.Status != "pending" || o.PriceCents != 3900 {
		t.Fatalf("返回解析错误: %+v", o)
	}
}

// 等级与返佣规则（契约 v1.2）：后台可配置，客户端只渲染 levels/rules 不自行判定。
func TestReferralParsesLevelsAndRules(t *testing.T) {
	var path string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": "DUK-ABC12", "level": 2, "levelName": "舵手", "commissionPct": 20,
			"onlineHours": 150.5, "orders": 4, "earnedCents": 1800,
			"balanceCents": 1800, "withdrawableCents": 0,
			"nextLevelHint": "再有效在线 150 小时 或 再成交 6 单 升到 Lv3 船长",
			"levels": []map[string]any{
				{"level": 1, "name": "船夫", "minHours": 0, "minOrders": 0, "commissionPct": 15},
				{"level": 2, "name": "舵手", "minHours": 100, "minOrders": 3, "commissionPct": 20},
				{"level": 3, "name": "船长", "minHours": 300, "minOrders": 10, "commissionPct": 25},
			},
			"rules": map[string]any{
				"commissionBase": "order_amount", "settlePeriod": "monthly",
				"withdrawMinCents": 5000, "allowSecondLevel": false,
				"secondLevelPct": 0, "trialCommission": false, "payoutNote": "按月结算",
			},
		})
	})
	c.SetSession(Session{AccessToken: "tok", Email: "a@b.com"})
	r, err := c.Referral()
	if err != nil {
		t.Fatalf("Referral: %v", err)
	}
	if path != "/.cloud/database/rest/rpc/referral_snapshot" {
		t.Fatalf("RPC 路径错误: %s", path)
	}
	if len(r.Levels) != 3 || r.Levels[1].MinHours != 100 || r.Levels[2].CommissionPct != 25 {
		t.Fatalf("等级规则解析错误: %+v", r.Levels)
	}
	if r.Rules.SettlePeriod != "monthly" || r.Rules.WithdrawMinCents != 5000 || r.Rules.AllowSecondLevel {
		t.Fatalf("返佣规则解析错误: %+v", r.Rules)
	}
}
