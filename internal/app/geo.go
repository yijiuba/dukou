package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yijiuba/dukou/internal/store"
)

// 服务器地区与服务商自动识别。
//
// 用户添加服务器只填 IP/SSH，界面上「新加坡 · 阿里云」这种名称和旗帜图标
// 原先全靠手工填——新加的服务器就是一行裸 IP。这里按 IP 归属自动回填：
// 名称 = 城市 · 服务商简称，图标 = 国旗 emoji。
//
// 识别失败（断网、限流、内网 IP）一律静默保留原值，绝不因为识别不了
// 而报错——这是锦上添花的功能，不是关键路径。

// geoAPI 免费地理查询（无鉴权，仅 HTTP；查询内容只有 IP，无隐私风险）。
const geoAPI = "http://ip-api.com/json/%s?lang=zh-CN&fields=status,country,city,isp,countryCode"

type geoInfo struct {
	Status      string `json:"status"`
	Country     string `json:"country"`
	CountryCode string `json:"countryCode"`
	City        string `json:"city"`
	ISP         string `json:"isp"`
}

// fetchGeo 查询 IP 归属。失败返回 ok=false，调用方保留原值。
func fetchGeo(ip string) (geoInfo, bool) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(fmt.Sprintf(geoAPI, ip))
	if err != nil {
		return geoInfo{}, false
	}
	defer resp.Body.Close()

	var g geoInfo
	if err := json.NewDecoder(resp.Body).Decode(&g); err != nil {
		return geoInfo{}, false
	}
	if g.Status != "success" {
		return geoInfo{}, false
	}
	return g, true
}

// flagEmoji 国家码转国旗 emoji（区域指示符：A-U+1F1E6 起的字母对）。
func flagEmoji(cc string) string {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	if len(cc) != 2 {
		return ""
	}
	var out []rune
	for _, r := range cc {
		if r < 'A' || r > 'Z' {
			return ""
		}
		out = append(out, 0x1F1E6+rune(r-'A'))
	}
	return string(out)
}

// providerShort 从 ISP 全称提炼简短服务商名，与界面上「阿里云」这类叫法对齐。
func providerShort(isp string) string {
	s := strings.ToLower(isp)
	switch {
	case strings.Contains(s, "alibaba") || strings.Contains(isp, "阿里"):
		return "阿里云"
	case strings.Contains(s, "tencent") || strings.Contains(isp, "腾讯"):
		return "腾讯云"
	case strings.Contains(s, "huawei") || strings.Contains(isp, "华为"):
		return "华为云"
	case strings.Contains(s, "amazon") || strings.Contains(s, "aws"):
		return "AWS"
	case strings.Contains(s, "google"):
		return "谷歌云"
	case strings.Contains(s, "microsoft") || strings.Contains(s, "azure"):
		return "微软云"
	case strings.Contains(s, "oracle"):
		return "甲骨文云"
	case strings.Contains(s, "vultr"):
		return "Vultr"
	case strings.Contains(s, "digitalocean"):
		return "DigitalOcean"
	case strings.Contains(s, "linode"):
		return "Linode"
	case strings.Contains(s, "hetzner"):
		return "Hetzner"
	case strings.Contains(isp, "电信") || strings.Contains(s, "telecom") || strings.Contains(s, "chinanet"):
		return "中国电信"
	case strings.Contains(isp, "联通") || strings.Contains(s, "unicom"):
		return "中国联通"
	case strings.Contains(isp, "移动") || strings.Contains(s, "cmcc"):
		return "中国移动"
	}
	// 认不出的取前 12 个字符截断，总比「未知」有信息量
	isp = strings.TrimSpace(isp)
	if isp == "" {
		return "未知服务商"
	}
	r := []rune(isp)
	if len(r) > 12 {
		return string(r[:12]) + "…"
	}
	return isp
}

// geoName 组装显示名称：城市 · 服务商（城市为空退到国家）。
func geoName(g geoInfo) string {
	place := g.City
	if place == "" {
		place = g.Country
	}
	if place == "" {
		return ""
	}
	return place + " · " + providerShort(g.ISP)
}

// RefreshServerGeo 按 IP 归属回填服务器名称与旗帜图标。
//
// 只补缺失，不覆盖用户输入：图标为空才回填图标；名称还是默认的裸 IP
// 才改成识别结果，用户起过的名字一律不动。
func (s *Service) RefreshServerGeo(id string) (store.Server, error) {
	srv, ok := s.store.Get(id)
	if !ok {
		return store.Server{}, fmt.Errorf("找不到服务器 %s，可能已被删除，请刷新列表", id)
	}
	if srv.Flag != "" && srv.Name != srv.Host {
		return srv, nil // 已识别过或用户已自定义，尊重现状
	}

	g, ok := fetchGeo(srv.Host)
	if !ok {
		return srv, nil // 识别失败静默保留，下次部署/添加成功还会尝试
	}

	in := serverInputFrom(srv)
	if srv.Flag == "" {
		in.Flag = flagEmoji(g.CountryCode)
	}
	if srv.Name == srv.Host {
		if name := geoName(g); name != "" {
			in.Name = name
		}
	}
	return s.store.Update(id, in)
}

// serverInputFrom 把现有服务器还原成 Update 可用的输入（凭据留空=保留）。
// ProxyUser 不在输入里：它由部署结果决定，normalize 会按现状回填。
func serverInputFrom(srv store.Server) store.ServerInput {
	return store.ServerInput{
		Name:       srv.Name,
		Flag:       srv.Flag,
		Host:       srv.Host,
		SSHPort:    srv.SSHPort,
		SSHUser:    srv.SSHUser,
		AuthType:   srv.AuthType,
		ProxyPort:  srv.ProxyPort,
		ServerPort: srv.ServerPort,
	}
}
