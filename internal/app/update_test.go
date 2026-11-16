package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.0", "0.1.0", 1},
		{"v0.2.0", "0.2.0", 0},
		{"0.2", "0.2.0", 0},
		{"0.10.0", "0.9.9", 1},
		{"1.0.0", "0.99.99", 1},
		{"0.2.0-dev", "0.2.0", -1},      // 预发布小于正式版
		{"0.2.0", "0.2.0-rc.1", 1},      // 正式版大于候选版
		{"0.2.0-rc.2", "0.2.0-rc.1", 1}, // 数字段比较
		{"0.2.0-beta", "0.2.0-rc.1", -1},
		{"0.2.0+build7", "0.2.0", 0}, // 构建元数据不参与
		{"0.1.9", "0.2.0", -1},
	}
	for _, c := range cases {
		if got := compareVersion(c.a, c.b); got != c.want {
			t.Errorf("compareVersion(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCheckUpdateParsesRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); !strings.HasPrefix(got, "dukou-desktop") {
			t.Errorf("缺少 User-Agent: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"tag_name":"v0.3.0",
			"name":"0.3.0",
			"body":"## 新增\n- 账号体系",
			"html_url":"https://github.com/yijiuba/dukou/releases/tag/v0.3.0",
			"prerelease":false,
			"published_at":"2026-12-01T08:00:00Z"
		}`))
	}))
	defer srv.Close()

	oldAPI, oldVer := releaseAPI, Version
	releaseAPI, Version = srv.URL, "0.2.0"
	defer func() { releaseAPI, Version = oldAPI, oldVer }()

	info, err := (&Service{}).CheckUpdate()
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if !info.Available || info.Latest != "0.3.0" || info.Current != "0.2.0" {
		t.Fatalf("结果不对: %+v", info)
	}
	if !strings.Contains(info.Notes, "账号体系") {
		t.Fatalf("更新说明未解析: %q", info.Notes)
	}
	if !strings.Contains(info.DownloadURL, "releases/tag/v0.3.0") {
		t.Fatalf("下载地址不对: %q", info.DownloadURL)
	}
}

func TestCheckUpdateNoNewVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.2.0","body":"whatever"}`))
	}))
	defer srv.Close()

	oldAPI, oldVer := releaseAPI, Version
	releaseAPI, Version = srv.URL, "0.2.0"
	defer func() { releaseAPI, Version = oldAPI, oldVer }()

	info, err := (&Service{}).CheckUpdate()
	if err != nil {
		t.Fatal(err)
	}
	if info.Available {
		t.Fatalf("同版本不应提示更新: %+v", info)
	}
}

func TestCheckUpdateHandlesRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	oldAPI := releaseAPI
	releaseAPI = srv.URL
	defer func() { releaseAPI = oldAPI }()

	_, err := (&Service{}).CheckUpdate()
	if err == nil || !strings.Contains(err.Error(), "频繁") {
		t.Fatalf("403 应翻译成限流提示，实际: %v", err)
	}
}

func TestCheckUpdateNetworkFailureIsUserFacing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // 立刻关掉，制造连接失败

	oldAPI := releaseAPI
	releaseAPI = url
	defer func() { releaseAPI = oldAPI }()

	_, err := (&Service{}).CheckUpdate()
	if err == nil {
		t.Fatal("网络失败应返回错误")
	}
	if !strings.Contains(err.Error(), "GitHub") && !strings.Contains(err.Error(), "网络") {
		t.Fatalf("错误文案不够面向用户: %v", err)
	}
}
