package app

import (
	"strings"
	"testing"
	"time"
)

// 分数口径（§六）：全 pass=100，每 fail 扣 20，warn 扣 8，skip 不计。
func TestDoctorScore(t *testing.T) {
	cases := []struct {
		name   string
		layers []DoctorLayer
		want   int
	}{
		{"全过", []DoctorLayer{
			{ID: "L1", Status: "pass"}, {ID: "L2", Status: "pass"},
		}, 100},
		{"一个 fail", []DoctorLayer{
			{ID: "L1", Status: "pass"}, {ID: "L2", Status: "fail"},
		}, 80},
		{"两个 warn", []DoctorLayer{
			{ID: "L1", Status: "warn"}, {ID: "L2", Status: "warn"},
		}, 84},
		{"skip 不计", []DoctorLayer{
			{ID: "L1", Status: "pass"}, {ID: "L2", Status: "skip"}, {ID: "L3", Status: "skip"},
		}, 100},
		{"混合", []DoctorLayer{
			{ID: "L1", Status: "pass"}, {ID: "L2", Status: "pass"},
			{ID: "L3", Status: "warn"}, {ID: "L4", Status: "pass"},
			{ID: "L5", Status: "skip"},
		}, 92},
		{"两个 fail 不为负", []DoctorLayer{
			{ID: "L1", Status: "fail"}, {ID: "L2", Status: "fail"},
			{ID: "L3", Status: "fail"}, {ID: "L4", Status: "fail"},
			{ID: "L5", Status: "fail"}, {ID: "L6", Status: "fail"},
		}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := DoctorReport{Layers: c.layers}
			r.finish(time.Now(), true)
			if r.Score != c.want {
				t.Errorf("分数 = %d, 期望 %d", r.Score, c.want)
			}
		})
	}
}

// 分层口径：非当前线路 L5~L8 必须是 skip 且提示切线路。
func TestDoctorNonCurrentLayersSkip(t *testing.T) {
	layers := remoteLayers(false)
	seen := map[string]DoctorLayer{}
	for _, l := range layers {
		seen[l.ID] = l
	}
	for _, id := range []string{"L5", "L6", "L7", "L8"} {
		l := seen[id]
		if l.Status != "skip" {
			t.Errorf("%s 非当前线路应为 skip，实际 %s", id, l.Status)
		}
		if !strings.Contains(l.Summary, "需切到该线路") {
			t.Errorf("%s 的提示应包含「需切到该线路」，实际 %q", id, l.Summary)
		}
	}
}

// probeViaProxy 对不可达代理必须返回 "000" 而不是 panic 或空串。
func TestProbeViaProxyUnreachable(t *testing.T) {
	if got, _ := probeViaProxy("http://127.0.0.1:1", "https://api.ipify.org"); got != "000" {
		t.Errorf("不可达代理应返回 000，实际 %q", got)
	}
	if got, _ := probeViaProxy("::::bad-url", "https://api.ipify.org"); got != "000" {
		t.Errorf("坏 URL 应返回 000，实际 %q", got)
	}
}
