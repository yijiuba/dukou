package app

import (
	"testing"

	"github.com/yijiuba/dukou/internal/store"
)

func TestFlagEmoji(t *testing.T) {
	cases := map[string]string{
		"SG": "🇸🇬",
		"us": "🇺🇸",
		"JP": "🇯🇵",
		"":   "",
		"S":  "",   // 长度不对
		"S1": "",   // 非字母
	}
	for cc, want := range cases {
		if got := flagEmoji(cc); got != want {
			t.Errorf("flagEmoji(%q) = %q, want %q", cc, got, want)
		}
	}
}

func TestProviderShort(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Alibaba Cloud LLC", "阿里云"},
		{"阿里巴巴", "阿里云"},
		{"Tencent Cloud Computing", "腾讯云"},
		{"HUAWEI CLOUD", "华为云"},
		{"Amazon.com, Inc.", "AWS"},
		{"Google LLC", "谷歌云"},
		{"Microsoft Azure", "微软云"},
		{"China Telecom", "中国电信"},
		{"China Unicom", "中国联通"},
		{"Some Unknown Hosting Ltd", "Some Unknown…"},
		{"", "未知服务商"},
	}
	for _, c := range cases {
		if got := providerShort(c.in); got != c.want {
			t.Errorf("providerShort(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGeoName(t *testing.T) {
	g := geoInfo{City: "Singapore", Country: "Singapore", ISP: "Alibaba Cloud"}
	if got := geoName(g); got != "Singapore · 阿里云" {
		t.Errorf("geoName = %q", got)
	}
	// 城市为空退到国家
	g2 := geoInfo{Country: "美国", ISP: "Amazon"}
	if got := geoName(g2); got != "美国 · AWS" {
		t.Errorf("geoName(no city) = %q", got)
	}
	// 国家也为空则不生成名称
	g3 := geoInfo{ISP: "Alibaba Cloud"}
	if got := geoName(g3); got != "" {
		t.Errorf("geoName(no place) = %q, want empty", got)
	}
}

func TestRefreshServerGeoKeepsUserInput(t *testing.T) {
	s := newTestService(t)
	srv, err := s.store.Add(store.ServerInput{
		Name:        "我的美国机",
		Flag:        "🇺🇸",
		Host:        "10.0.0.9",
		AuthType:    "password",
		SSHPassword: "pw",
		ProxyPort:   7890,
		ServerPort:  8443,
		Status:      "pending",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	// 用户已自定义名称与图标 → 识别不覆盖（fetchGeo 对内网 IP 必失败，正好验证静默路径）
	upd, err := s.RefreshServerGeo(srv.ID)
	if err != nil {
		t.Fatalf("RefreshServerGeo: %v", err)
	}
	if upd.Name != "我的美国机" || upd.Flag != "🇺🇸" {
		t.Errorf("自定义的名称/图标被改动: %q/%q", upd.Name, upd.Flag)
	}
	// 状态等字段不被识别流程动到
	if upd.Status != "pending" {
		t.Errorf("状态被改动: %q → %q", "pending", upd.Status)
	}
}
