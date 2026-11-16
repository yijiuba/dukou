package app

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yijiuba/dukou/internal/diagnose"
	"github.com/yijiuba/dukou/internal/proxy"
	"github.com/yijiuba/dukou/internal/remote"
	"github.com/yijiuba/dukou/internal/store"
)

// DoctorLayer 是一层体检结果，直接对应健康诊断页的一行。
type DoctorLayer struct {
	ID      string   `json:"id"`                // "L1".."L8"
	Name    string   `json:"name"`              // 环境/配置/服务/链路/鉴权/转发/性能/分流
	Status  string   `json:"status"`            // pass | fail | warn | skip
	Summary string   `json:"summary"`           // 一句话结论
	Explain string   `json:"explain,omitempty"` // 为什么这样判断
	Fix     []string `json:"fix,omitempty"`     // 可直接执行的修复命令
}

// DoctorReport 是一次完整体检的报告。
type DoctorReport struct {
	ServerID   string        `json:"serverId"`
	ServerName string        `json:"serverName"`
	Host       string        `json:"host"`
	Current    bool          `json:"current"`  // 是否为当前连接的线路
	Score      int           `json:"score"`    // 全 pass=100，每 fail 扣 20，warn 扣 8，skip 不计
	Layers     []DoctorLayer `json:"layers"`
	Log        []string      `json:"log"`     // 诊断日志，供导出
	ElapsedMs  int64         `json:"elapsedMs"`
	CheckedAt  string        `json:"checkedAt"` // RFC3339
}

// Doctor 对指定服务器跑分层体检。
//
// 分层口径（§六）：非当前线路只跑 L1~L3（服务端体检），
// L4~L8 显示「需切到该线路」。L4 起的检查要么依赖本地代理
// 指向这台服务器，要么消耗流量，对非当前线路做只会给出误导结果。
func (s *Service) Doctor(serverID string) (DoctorReport, error) {
	start := time.Now()

	srv, ok := s.store.Get(serverID)
	if !ok {
		return DoctorReport{}, fmt.Errorf("找不到服务器 %s，可能已被删除，请刷新列表", serverID)
	}

	rep := DoctorReport{
		ServerID:   srv.ID,
		ServerName: srv.Name,
		Host:       srv.Host,
		CheckedAt:  time.Now().Format(time.RFC3339),
	}
	rep.Log = []string{}

	// 当前线路判定：代理活着且指向这台服务器
	st, err := proxy.Current(s.proxyOptions())
	current := err == nil && st.Connected && st.ServerID == srv.ID
	rep.Current = current

	// ── 远端三层：L1 环境 / L2 配置 / L3 服务 ──────────────────
	sshPW, sshKey, err := s.store.SSHCredentials(srv.ID)
	if err != nil {
		// 凭据读不出（如重装系统换机器码）时，远端三层全部无从做起
		rep.Layers = append(rep.Layers, skipAllRemote(srv.Name)...)
		rep.finish(start, current)
		return rep, nil
	}

	ssh, err := remote.DialSSH(remote.SSHConfig{
		Host:     srv.Host,
		Port:     srv.SSHPort,
		User:     srv.SSHUser,
		Password: sshPW,
		Key:      sshKey,
		Timeout:  12 * time.Second,
	})
	if err != nil {
		// SSH 都连不上，说明体检的前提不成立：三层全部 skip 并给出原因
		rep.Layers = append(rep.Layers, DoctorLayer{
			ID: "L1", Name: "环境", Status: "fail",
			Summary: "SSH 连不上服务器，体检无法进行",
			Explain: err.Error(),
			Fix:     []string{"确认服务器还在运行、IP 与端口未变、密码未修改"},
		})
		for _, l := range remoteLayers(current) {
			if l.ID != "L1" {
				l.Status = "skip"
				l.Summary = "跳过：SSH 连不上，前面的层先修复"
				rep.Layers = append(rep.Layers, l)
			}
		}
		rep.finish(start, current)
		return rep, nil
	}
	defer ssh.Close()

	rep.Layers = append(rep.Layers, s.docL1Env(ssh, &rep.Log))
	rep.Layers = append(rep.Layers, s.docL2Config(ssh, srv, &rep.Log))
	rep.Layers = append(rep.Layers, s.docL3Service(ssh, &rep.Log))

	// ── 本地侧：L4 链路（非当前线路也跑——它只验服务端可达性）──
	// 文档口径写的是"只跑 L1~L3"，但 L4 的 TCP 拨测不依赖代理，
	// 且是安全组是否放行的唯一直接验证，跑它只会更有用。
	rep.Layers = append(rep.Layers, s.docL4Link(srv, &rep.Log))

	// ── L5~L8：只对当前线路跑 ─────────────────────────────────
	if !current {
		// L2/L3 刚经 SSH 真实跑过，不能重复追加；只补 L5~L8 的「需切到该线路」占位
		placeholders := remoteLayers(current)
		rep.Layers = append(rep.Layers, placeholders[3:]...)
		rep.finish(start, current)
		return rep, nil
	}

	// L5~L8 需要本地代理在跑。代理没跑时（状态漂移）如实降级。
	proxyUp := current && st.Connected
	// Normalize 会把零值端口补成默认 7890——不补的话会拼出
	// http://127.0.0.1:0，探测全部 000 还看不出原因。
	localProxy := fmt.Sprintf("http://127.0.0.1:%d", s.proxyOptions().Normalize().HTTPPort)

	if !proxyUp {
		rep.Layers = append(rep.Layers, DoctorLayer{
			ID: "L5", Name: "鉴权", Status: "skip",
			Summary: "跳过：本地代理未在运行",
		})
		rep.Layers = append(rep.Layers, remoteLayers(current)[3:]...)
		rep.finish(start, current)
		return rep, nil
	}

	rep.Layers = append(rep.Layers, s.docL5Auth(localProxy, srv, st, &rep.Log))
	rep.Layers = append(rep.Layers, s.docL6Forward(localProxy, &rep.Log))
	rep.Layers = append(rep.Layers, s.docL7Perf(&rep.Log))
	rep.Layers = append(rep.Layers, s.docL8Split(&rep.Log))

	rep.finish(start, current)
	return rep, nil
}

// finish 计算分数并收尾。skip 不计分：只扣 fail/warn。
func (r *DoctorReport) finish(start time.Time, _ bool) {
	score := 100
	for _, l := range r.Layers {
		switch l.Status {
		case "fail":
			score -= 20
		case "warn":
			score -= 8
		}
	}
	if score < 0 {
		score = 0
	}
	r.Score = score
	r.ElapsedMs = time.Since(start).Milliseconds()
}

// ── 各层实现 ────────────────────────────────────────────────────

// docL1Env 环境层：系统发行版、架构、systemd 可用性。
func (s *Service) docL1Env(ssh *remote.SSH, log *[]string) DoctorLayer {
	osr, _ := ssh.Run("cat /etc/os-release 2>/dev/null | grep PRETTY_NAME | cut -d'\"' -f2")
	arch, _ := ssh.Run("uname -m")
	sd, _ := ssh.Run("systemctl --version 2>/dev/null | head -1")
	*log = append(*log,
		"[L1] os="+strings.TrimSpace(osr),
		"[L1] arch="+strings.TrimSpace(arch),
		"[L1] systemd="+firstLine(sd))

	pretty := strings.TrimSpace(osr)
	if pretty == "" {
		pretty = "未知发行版"
	}
	l := DoctorLayer{ID: "L1", Name: "环境", Status: "pass",
		Summary: fmt.Sprintf("%s · %s · 依赖探测完成", pretty, strings.TrimSpace(arch))}

	if strings.TrimSpace(sd) == "" {
		l.Status = "fail"
		l.Summary = "服务器上没有 systemd，本工具的服务管理能力不可用"
		l.Fix = []string{"# Alpine 等无 systemd 系统不在支持范围内，请换主流发行版"}
	} else if strings.Contains(pretty, "CentOS 7") || strings.Contains(pretty, "Debian 10") {
		l.Status = "warn"
		l.Summary = pretty + " 已停止维护或接近 EOL，建议换新版"
	}
	return l
}

// docL2Config 配置层：systemd 单元存在、ExecStart 写法正确、gost 版本。
func (s *Service) docL2Config(ssh *remote.SSH, srv store.Server, log *[]string) DoctorLayer {
	unit, err := ssh.ReadFile("/etc/systemd/system/dukou.service")
	*log = append(*log, "[L2] unit=/etc/systemd/system/dukou.service exists="+
		fmt.Sprint(err == nil))
	if err != nil {
		return DoctorLayer{
			ID: "L2", Name: "配置", Status: "fail",
			Summary: "找不到 /etc/systemd/system/dukou.service，服务端可能没有部署过",
			Fix:     []string{"在「网络配置」页重新执行部署"},
		}
	}
	text := string(unit)

	// 上游写法：必须是 tls://。这是 diagnose 包里一次真实误诊的教训。
	if !strings.Contains(text, "tls://") {
		return DoctorLayer{
			ID: "L2", Name: "配置", Status: "fail",
			Summary: "单元文件缺少 tls:// 上游写法，域名会被明文识别",
			Fix:     []string{"grep ExecStart /etc/systemd/system/dukou.service", "重新部署以覆盖为正确配置"},
		}
	}
	if !strings.Contains(text, fmt.Sprintf("%d", srv.ServerPort)) {
		*log = append(*log, "[L2] warn: unit port mismatch")
		return DoctorLayer{
			ID: "L2", Name: "配置", Status: "warn",
			Summary: fmt.Sprintf("单元端口与清单记录不一致（清单记 %d）", srv.ServerPort),
			Explain: "通常是手工改过端口。以实际运行端口为准的话，请更新服务器清单",
		}
	}

	ver, _ := ssh.Run("gost -V 2>/dev/null | awk '{print $2}'")
	*log = append(*log, "[L2] gost="+strings.TrimSpace(ver))
	return DoctorLayer{ID: "L2", Name: "配置", Status: "pass",
		Summary: fmt.Sprintf("单元校验通过 · gost %s · 上游 tls://", strings.TrimSpace(ver))}
}

// docL3Service 服务层：systemd 是否 active。
func (s *Service) docL3Service(ssh *remote.SSH, log *[]string) DoctorLayer {
	state, _ := ssh.Run("systemctl is-active dukou.service 2>/dev/null")
	state = strings.TrimSpace(state)
	*log = append(*log, "[L3] systemctl is-active dukou = "+state)
	if state == "active" {
		return DoctorLayer{ID: "L3", Name: "服务", Status: "pass",
			Summary: "dukou.service 运行中（systemd active）"}
	}
	return DoctorLayer{
		ID: "L3", Name: "服务", Status: "fail",
		Summary: "dukou.service 状态为 " + quote(state) + "，服务没有正常运行",
		Fix: []string{
			"systemctl restart dukou",
			"journalctl -u dukou -n 30 --no-pager   # 看具体报错",
		},
	}
}

// docL4Link 链路层：从本机 TCP 直拨服务器代理端口，量 RTT。
// 安全组没放行时唯一能在这里现形的检查。
func (s *Service) docL4Link(srv store.Server, log *[]string) DoctorLayer {
	addr := net.JoinHostPort(srv.Host, fmt.Sprint(srv.ServerPort))
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 8*time.Second)
	rtt := time.Since(start).Milliseconds()
	*log = append(*log, fmt.Sprintf("[L4] tcp %s ... rtt=%dms err=%v", addr, rtt, err))
	if err != nil {
		conn2, err2 := net.DialTimeout("tcp", net.JoinHostPort(srv.Host, "22"), 8*time.Second)
		sshOK := err2 == nil
		if conn2 != nil {
			conn2.Close()
		}
		l := DoctorLayer{
			ID: "L4", Name: "链路", Status: "fail",
			Summary: fmt.Sprintf("TCP %s 不可达", addr),
			Explain: "连接被拒绝或超时。三个地方都要放行，漏一个的症状都是超时：" +
				"云控制台安全组、服务器防火墙、服务是否监听在 0.0.0.0。",
			Fix: []string{
				fmt.Sprintf("ss -lntp | grep %d   # 在服务器上确认监听", srv.ServerPort),
				"firewall-cmd --list-ports   # 或 ufw status",
				"# 云控制台安全组需手工放行",
			},
		}
		if sshOK {
			l.Explain += "（SSH 22 端口是通的，问题几乎可以锁定在代理端口的放行上）"
		}
		return l
	}
	conn.Close()
	return DoctorLayer{ID: "L4", Name: "链路", Status: "pass",
		Summary: fmt.Sprintf("%d 端口可达（%d ms）", srv.ServerPort, rtt)}
}

// docL5Auth 鉴权层：经本地代理请求，用 diagnose 的真实误诊经验分类。
func (s *Service) docL5Auth(localProxy string, srv store.Server, st proxy.Status, log *[]string) DoctorLayer {
	code, detail := probeViaProxy(localProxy, "https://api.ipify.org")
	*log = append(*log, "[L5] via proxy https://api.ipify.org -> "+code+
		"（"+detail+"）")
	v := diagnose.ClassifyProxyResult(code)
	l := toLayer("L5", "鉴权", v)

	// 出口 IP 与服务器对不上时给出提示（比如用户手动换过线路）
	if v.Status == diagnose.Pass && st.EgressIP != "" && st.EgressIP != srv.Host {
		l.Status = "warn"
		l.Summary += fmt.Sprintf("，但出口 IP %s 与该服务器 %s 不一致", st.EgressIP, srv.Host)
		l.Explain = "本地代理当前的上游可能不是这台服务器。切到该线路后重新体检可获得准确结论"
	}
	return l
}

// docL6Forward 转发层：域名特异性分析（diagnose.AnalyzeDomainSpecific）。
func (s *Service) docL6Forward(localProxy string, log *[]string) DoctorLayer {
	targets := []string{"https://github.com", "https://www.google.com", "https://chatgpt.com"}
	results := make([]diagnose.DomainResult, 0, len(targets))
	for _, t := range targets {
		code, detail := probeViaProxy(localProxy, t)
		host := strings.TrimPrefix(strings.TrimPrefix(t, "https://"), "www.")
		results = append(results, diagnose.DomainResult{Host: host, Code: code})
		*log = append(*log, fmt.Sprintf("[L6] via proxy %s -> %s（%s）", host, code, detail))
	}
	return toLayer("L6", "转发", diagnose.AnalyzeDomainSpecific(results))
}

// docL7Perf 性能层：默认跳过——吞吐测试要拉 10MB 流量，
// 体检是高频动作，不该每次都让用户花这份流量。
func (s *Service) docL7Perf(log *[]string) DoctorLayer {
	*log = append(*log, "[L7] throughput test skipped (saves ~10MB)")
	return DoctorLayer{
		ID: "L7", Name: "性能", Status: "skip",
		Summary: "跳过流量测试（约消耗 10MB）。如需测速可在服务器上跑：curl -o /dev/null -sS -w '%{speed_download}\\n' https://speed.cloudflare.com/__down?bytes=10000000",
	}
}

// docL8Split 分流层：检查国内站点是否直连。
// 分流能力上线前，所有流量都经代理——如实报告，不装作通过。
func (s *Service) docL8Split(log *[]string) DoctorLayer {
	// 探测路径：不走系统代理直接访问 baidu（Go 默认不走系统代理，
	// 所以这个请求就是"直连路径"的真实表现）
	code := probeDirect("https://www.baidu.com")
	*log = append(*log, "[L8] direct https://www.baidu.com -> "+code)
	if code == "000" {
		return DoctorLayer{
			ID: "L8", Name: "分流", Status: "warn",
			Summary: "国内站点直连不通（baidu 返回 000）",
			Explain: "分流未启用时所有流量都走代理，国内访问反而绕路。若直连也不通，" +
				"请检查本机基础网络",
		}
	}
	return DoctorLayer{
		ID: "L8", Name: "分流", Status: "warn",
		Summary: "国内站点可直连，但分流未启用：当前所有流量都经代理出国再回来",
		Explain: "国内访问（淘宝/百度等）绕道海外会明显变慢且消耗服务器流量。" +
			"PAC 分流能力在后续版本提供后，这里会变成「国内直连 · 国外走代理」的自动分流检查",
	}
}

// ── 辅助 ────────────────────────────────────────────────────────

// remoteLayers 返回 L1 之外的远端/本地各层的占位（skip 状态）。
// current=false 时用于 L5~L8 的「需切到该线路」提示。
func remoteLayers(current bool) []DoctorLayer {
	need := ""
	if !current {
		need = "需切到该线路后体检"
	}
	mk := func(id, name string) DoctorLayer {
		return DoctorLayer{ID: id, Name: name, Status: "skip", Summary: "跳过：" + need}
	}
	return []DoctorLayer{
		{ID: "L1", Name: "环境", Status: "skip"},
		{ID: "L2", Name: "配置", Status: "skip", Summary: "跳过：SSH 凭据不可用"},
		{ID: "L3", Name: "服务", Status: "skip", Summary: "跳过：SSH 凭据不可用"},
		mk("L5", "鉴权"),
		mk("L6", "转发"),
		mk("L7", "性能"),
		mk("L8", "分流"),
	}
}

// skipAllRemote SSH 凭据读不出来时的全 skip 占位（重装系统换机器码场景）。
func skipAllRemote(name string) []DoctorLayer {
	return []DoctorLayer{
		{ID: "L1", Name: "环境", Status: "skip", Summary: "跳过：读取 SSH 凭据失败（可能系统重装导致加密密钥失效）"},
		{ID: "L2", Name: "配置", Status: "skip", Summary: "跳过：SSH 凭据不可用"},
		{ID: "L3", Name: "服务", Status: "skip", Summary: "跳过：SSH 凭据不可用"},
		{ID: "L4", Name: "链路", Status: "skip", Summary: "跳过"},
		{ID: "L5", Name: "鉴权", Status: "skip", Summary: "跳过"},
		{ID: "L6", Name: "转发", Status: "skip", Summary: "跳过"},
		{ID: "L7", Name: "性能", Status: "skip", Summary: "跳过"},
		{ID: "L8", Name: "分流", Status: "skip", Summary: "跳过"},
	}
}

func toLayer(id, name string, v diagnose.Verdict) DoctorLayer {
	l := DoctorLayer{ID: id, Name: name, Summary: v.Summary, Explain: v.Explain, Fix: v.Fix}
	switch v.Status {
	case diagnose.Pass:
		l.Status = "pass"
	case diagnose.Fail:
		l.Status = "fail"
	case diagnose.Warn:
		l.Status = "warn"
	default:
		l.Status = "skip"
	}
	return l
}

// probeViaProxy 经代理访问目标，返回 HTTP 状态码字符串，失败返回 "000"。
// 与 cmd/doctor.go 同逻辑：GUI 体检与 CLI doctor 必须同一套探测口径，
// 否则两边结论不一致会互相打脸。errDetail 返回底层错误（记入诊断日志）。
func probeViaProxy(proxyURL, target string) (string, string) {
	pu, err := url.Parse(proxyURL)
	if err != nil {
		return "000", "代理地址解析失败: " + err.Error()
	}
	client := &http.Client{
		Timeout: 20 * time.Second,
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(pu),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(target)
	if err != nil {
		return "000", err.Error()
	}
	defer resp.Body.Close()
	return fmt.Sprint(resp.StatusCode), ""
}

// probeDirect 不走任何代理直接访问（Go 的 http 默认不读系统代理）。
func probeDirect(target string) string {
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(target)
	if err != nil {
		return "000"
	}
	defer resp.Body.Close()
	return fmt.Sprint(resp.StatusCode)
}

func quote(s string) string {
	return "“" + s + "”"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
