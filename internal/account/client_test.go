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
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-wb-webapp-access-key") != "wbpk_test" {
			atomic.AddInt32(&missing, 1)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"verification_id": "vid", "is_user": false})
	})
	if _, err := c.RequestCode("a@b.com"); err != nil {
		t.Fatalf("RequestCode: %v", err)
	}
	if missing != 0 {
		t.Fatalf("有 %d 个请求没带 x-wb-webapp-access-key", missing)
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
