package diagnose

import (
	"strings"
	"testing"
)

func TestClassifyProxyResult(t *testing.T) {
	cases := []struct {
		code   string
		status Status
		// 结论里必须出现的关键词，用于确认「说清了是什么问题」
		mustContain string
	}{
		{"200", Pass, "正常"},
		{"301", Pass, "正常"},
		{"204", Pass, "正常"},
		{"407", Fail, "密码"},
		{"403", Warn, "风控"},
		{"503", Fail, "gost"},
		{"000", Fail, "000"},
		{"", Fail, "000"},
	}
	for _, tc := range cases {
		t.Run("code="+tc.code, func(t *testing.T) {
			v := ClassifyProxyResult(tc.code)
			if v.Status != tc.status {
				t.Errorf("状态应为 %v，实际 %v", tc.status, v.Status)
			}
			joined := v.Summary + v.Explain
			if !strings.Contains(joined, tc.mustContain) {
				t.Errorf("结论应包含 %q，实际: %s", tc.mustContain, joined)
			}
		})
	}
}

// 407 必须给出「先核对密码」的指引，而不是让人去查网络。
// 这是本次排障中代价最大的一次误判。
func TestClassify407PointsToPassword(t *testing.T) {
	v := ClassifyProxyResult("407")
	fix := strings.Join(v.Fix, " ")
	if !strings.Contains(fix, "dukou.service") {
		t.Errorf("修复建议应指向两端的服务配置: %s", fix)
	}
	if strings.Contains(fix, "MTU") || strings.Contains(fix, "防火墙") {
		t.Errorf("407 不应引导用户去查网络: %s", fix)
	}
}

// 000 必须提醒「可能是 -s 吞掉了错误」，否则用户会以为断网。
func TestClassify000WarnsAboutSilentFlag(t *testing.T) {
	v := ClassifyProxyResult("000")
	if !strings.Contains(v.Explain, "-sS") {
		t.Errorf("000 的解释必须提醒用 -sS 而不是 -s: %s", v.Explain)
	}
}

func TestAnalyzeDomainSpecificAllOK(t *testing.T) {
	rs := []DomainResult{
		{"github.com", "200"},
		{"google.com", "200"},
		{"chatgpt.com", "403"},
	}
	v := AnalyzeDomainSpecific(rs)
	if v.Status != Pass {
		t.Errorf("全部可达应为 Pass，实际 %v: %s", v.Status, v.Summary)
	}
}

func TestAnalyzeDomainSpecificAllFail(t *testing.T) {
	rs := []DomainResult{
		{"github.com", "000"},
		{"google.com", "000"},
	}
	v := AnalyzeDomainSpecific(rs)
	if v.Status != Fail {
		t.Errorf("全部失败应为 Fail，实际 %v", v.Status)
	}
	if !strings.Contains(v.Summary, "整体") {
		t.Errorf("全不通应判为整体故障，实际: %s", v.Summary)
	}
}

// 这是本次排障最核心的一次判断：github 通、google 不通 → 明文域名被识别。
func TestAnalyzeDomainSpecificPartialFailureMeansTLS(t *testing.T) {
	rs := []DomainResult{
		{"github.com", "200"},
		{"google.com", "000"},
		{"chatgpt.com", "000"},
	}
	v := AnalyzeDomainSpecific(rs)

	if v.Status != Fail {
		t.Fatalf("部分失败应为 Fail，实际 %v", v.Status)
	}
	if !strings.Contains(v.Summary, "明文") {
		t.Errorf("结论应指出明文 CONNECT 被识别，实际: %s", v.Summary)
	}
	if !strings.Contains(v.Summary, "google.com") {
		t.Errorf("结论应点名失败的域名，实际: %s", v.Summary)
	}

	fix := strings.Join(v.Fix, " ")
	if !strings.Contains(fix, "http+tls") {
		t.Errorf("修复建议应包含服务端改 http+tls: %s", fix)
	}
	if !strings.Contains(fix, "-F tls://") {
		t.Errorf("修复建议应包含客户端改 -F tls://: %s", fix)
	}

	// 必须明确排除掉那几条错误方向，否则用户会重走弯路
	for _, wrong := range []string{"DNS", "MTU"} {
		if !strings.Contains(v.Explain, wrong) {
			t.Errorf("解释中应明确排除 %s：%s", wrong, v.Explain)
		}
	}
}

func TestAnalyzeDomainSpecificEmpty(t *testing.T) {
	v := AnalyzeDomainSpecific(nil)
	if v.Status != Skip {
		t.Errorf("无数据时应 Skip，实际 %v", v.Status)
	}
}

// 23KB/s + 13% 丢包 + 155ms —— 这是真实测到的组合，
// 当时差点误判为带宽不足。必须判为丢包瓶颈并推荐 BBR。
func TestAnalyzeThroughputRealCase(t *testing.T) {
	v := AnalyzeThroughput(PerfInput{
		BytesPerSecond:    23175,
		LossPct:           13,
		RTTMs:             155,
		CongestionControl: "cubic",
	})

	if v.Status != Fail {
		t.Fatalf("应判为 Fail，实际 %v: %s", v.Status, v.Summary)
	}
	if !strings.Contains(v.Summary, "丢包") {
		t.Errorf("结论应指出丢包是瓶颈，实际: %s", v.Summary)
	}
	if !strings.Contains(v.Explain, "不是带宽") {
		t.Errorf("必须明确说明不是带宽问题，实际: %s", v.Explain)
	}
	if !strings.Contains(v.Explain, "反算") {
		t.Errorf("应展示公式反算过程作为证据，实际: %s", v.Explain)
	}

	fix := strings.Join(v.Fix, " ")
	for _, must := range []string{"tcp_bbr", "tcp_congestion_control=bbr"} {
		if !strings.Contains(fix, must) {
			t.Errorf("修复建议应包含 %s: %s", must, fix)
		}
	}
}

// 开 BBR 后实测 1.65MB/s —— 应判为健康。
func TestAnalyzeThroughputAfterBBR(t *testing.T) {
	v := AnalyzeThroughput(PerfInput{
		BytesPerSecond:    1651855,
		LossPct:           12,
		RTTMs:             156,
		CongestionControl: "bbr",
	})
	if v.Status != Pass {
		t.Fatalf("1.65MB/s 应判为 Pass，实际 %v: %s", v.Status, v.Summary)
	}
	if !strings.Contains(v.Summary, "Mbps") {
		t.Errorf("应换算成 Mbps 便于理解: %s", v.Summary)
	}
}

// BBR 已开但仍慢时，不应再让用户重复开 BBR，而应指向换协议/换地域。
func TestAnalyzeThroughputBBRAlreadyOn(t *testing.T) {
	v := AnalyzeThroughput(PerfInput{
		BytesPerSecond:    25000,
		LossPct:           10,
		RTTMs:             200,
		CongestionControl: "bbr",
	})
	fix := strings.Join(v.Fix, " ")
	if strings.Contains(fix, "modprobe tcp_bbr") {
		t.Errorf("BBR 已开启时不应再给开启命令: %s", fix)
	}
	if !strings.Contains(v.Explain, "Hysteria2") {
		t.Errorf("BBR 无效时应指向 Hysteria2: %s", v.Explain)
	}
}

// 丢包低却慢 —— 这才可能是带宽问题，指向控制台查规格。
func TestAnalyzeThroughputLowLossSlowMeansBandwidth(t *testing.T) {
	v := AnalyzeThroughput(PerfInput{
		BytesPerSecond:    30000,
		LossPct:           0.5,
		RTTMs:             50,
		CongestionControl: "bbr",
	})
	if v.Status != Warn {
		t.Errorf("应判为 Warn，实际 %v", v.Status)
	}
	if !strings.Contains(v.Explain, "带宽") {
		t.Errorf("丢包低时应指向带宽: %s", v.Explain)
	}
}

func TestAnalyzeThroughputMidRange(t *testing.T) {
	v := AnalyzeThroughput(PerfInput{BytesPerSecond: 100 * 1024, LossPct: 1, RTTMs: 100})
	if v.Status != Warn {
		t.Errorf("100KB/s 应判为 Warn，实际 %v", v.Status)
	}
}

func TestParseSpeedOutput(t *testing.T) {
	cases := map[string]float64{
		"1651855":       1651855,
		"  23175\n":     23175,
		"0":             0,
		"":              0,
		"not-a-number":  0,
		"123.5":         123.5,
	}
	for in, want := range cases {
		if got := ParseSpeedOutput(in); got != want {
			t.Errorf("ParseSpeedOutput(%q) = %v，应为 %v", in, got, want)
		}
	}
}

func TestSqrt(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{0, 0},
		{-1, 0},
		{4, 2},
		{9, 3},
		{100, 10},
	}
	for _, c := range cases {
		got := sqrt(c.in)
		diff := got - c.want
		if diff < 0 {
			diff = -diff
		}
		if diff > 1e-9 {
			t.Errorf("sqrt(%v) = %v，应为 %v", c.in, got, c.want)
		}
	}
}

func TestStatusSymbol(t *testing.T) {
	if Pass.Symbol() != "[ ok ]" {
		t.Errorf("Pass 符号错误: %s", Pass.Symbol())
	}
	if Fail.Symbol() != "[fail]" {
		t.Errorf("Fail 符号错误: %s", Fail.Symbol())
	}
	if Warn.Symbol() != "[warn]" {
		t.Errorf("Warn 符号错误: %s", Warn.Symbol())
	}
	if Skip.Symbol() != "[skip]" {
		t.Errorf("Skip 符号错误: %s", Skip.Symbol())
	}
}
