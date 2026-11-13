package cmd

import (
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yijiuba/dukou/internal/config"
	"github.com/yijiuba/dukou/internal/detect"
	"github.com/yijiuba/dukou/internal/diagnose"
	"github.com/yijiuba/dukou/internal/systemd"
)

type doctorOptions struct {
	server   string
	port     int
	confPath string
	full     bool // 是否跑性能测试（会消耗流量，默认跳过）
}

func runDoctor(args []string) error {
	var o doctorOptions
	fs := flag.NewFlagSet("dukou doctor", flag.ContinueOnError)
	fs.StringVar(&o.server, "server", "", "出口服务器地址，覆盖配置文件")
	fs.IntVar(&o.port, "port", 0, "出口服务器端口，覆盖配置文件")
	fs.StringVar(&o.confPath, "conf", config.DefaultPath(), "配置文件路径")
	fs.BoolVar(&o.full, "full", false, "额外跑吞吐测试（会消耗约 10MB 流量）")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fmt.Println("dukou doctor ─ 分层诊断")
	fmt.Println()
	fmt.Println("  逐层往下测，某层失败即为断点，后面的层不再有意义。")
	fmt.Println("  每层都给出「是什么问题」和「怎么修」，而不只是罗列数字。")

	failed := 0

	// 非 Linux 平台无法部署服务，也无法读 /etc 下的配置与 systemd 状态。
	// 这种情况下把「读不到配置」判为失败会误导用户，因此整体降级为有限诊断。
	onLinux := runtime.GOOS == "linux"
	if !onLinux {
		fmt.Printf("\n注意：当前平台是 %s，dukou 的部署与服务管理只支持 Linux。\n", runtime.GOOS)
		fmt.Println("      以下只做环境探测，其余各层跳过。")
	}

	// ── L1 环境 ─────────────────────────────────────────────────
	layerHeader(1, "环境")
	profile := detect.Detect(detect.NewEnv())
	for _, line := range profile.Lines() {
		detail("%s", line)
	}
	if !onLinux {
		// 非 Linux 上 Init 必然是 none，不应作为告警
	} else if profile.Init != detect.Systemd {
		verdict(diagnose.Verdict{
			Status:  diagnose.Warn,
			Summary: "初始化系统不是 systemd，dukou 的服务管理功能不可用",
			Fix:     []string{"# Alpine 等系统需要手工用 OpenRC 托管 gost"},
		})
		failed++
	}

	if !onLinux {
		layerHeader(2, "配置 / 服务 / 链路 / 性能")
		verdict(diagnose.Verdict{
			Status:  diagnose.Skip,
			Summary: "全部跳过：这些检查只对 Linux 目标机器有意义",
			Explain: "dukou 的 server / client 子命令在 Linux 上运行。" +
				"在 Windows 或 macOS 上只能查看帮助与做代码检查。",
		})
		printDoctorFooter(failed)
		return nil
	}

	// ── L2 配置 ─────────────────────────────────────────────────
	layerHeader(2, "配置")
	cfg, cfgErr := config.Load(o.confPath)
	switch {
	case cfgErr != nil:
		verdict(diagnose.Verdict{
			Status:  diagnose.Fail,
			Summary: fmt.Sprintf("读不到配置 %s", o.confPath),
			Explain: "还没部署过，或配置文件被删了。若已手工配置过代理，可用 " +
				"--server/--port 直接指定参数做后续诊断。",
			Fix: []string{
				"dukou server            # 在出口服务器上部署",
				"dukou client --server IP # 在受控机器上部署",
			},
		})
		failed++
	default:
		detail("%s：角色 %s，端口 %d，用户 %s", o.confPath, cfg.Role, cfg.Port, cfg.User)
	}

	// 命令行参数覆盖配置
	server := o.server
	if server == "" {
		server = cfg.Server
	}
	port := o.port
	if port == 0 {
		port = cfg.Port
	}

	// ── L3 服务 ─────────────────────────────────────────────────
	layerHeader(3, "服务")
	role := string(cfg.Role)
	if role == "" {
		role = "client"
	}
	svcName := systemd.ServiceName(role)
	svcState := systemctlState(svcName)
	switch svcState {
	case "active":
		detail("%s.service 运行中", svcName)
	default:
		verdict(diagnose.Verdict{
			Status:  diagnose.Fail,
			Summary: fmt.Sprintf("%s.service 状态为 %q，服务没有正常运行", svcName, svcState),
			Fix: []string{
				fmt.Sprintf("systemctl restart %s", svcName),
				fmt.Sprintf("journalctl -u %s -n 30 --no-pager   # 看具体报错", svcName),
			},
		})
		failed++
	}

	// ── L4 链路 ─────────────────────────────────────────────────
	layerHeader(4, "链路")
	if role == "server" {
		// 服务端自检：本机 TLS 握手必须拿到 407
		code, err := probeLocalTLS(port)
		if err != nil {
			verdict(diagnose.Verdict{
				Status:  diagnose.Fail,
				Summary: fmt.Sprintf("本机 https://127.0.0.1:%d 连不上", port),
				Explain: "进程在跑但端口没响应，通常是端口被别的进程占用、或监听地址不对。",
				Fix: []string{
					fmt.Sprintf("ss -lntp | grep %d", port),
					fmt.Sprintf("journalctl -u %s -n 30 --no-pager", svcName),
				},
			})
			failed++
		} else {
			v := diagnose.ClassifyProxyResult(code)
			verdict(v)
			if v.Status == diagnose.Fail {
				failed++
			}
		}
	} else {
		if server == "" {
			verdict(diagnose.Verdict{
				Status:  diagnose.Skip,
				Summary: "跳过：不知道出口服务器地址",
				Fix:     []string{"dukou doctor --server 你的ECS地址 --port 8443"},
			})
		} else {
			addr := net.JoinHostPort(server, fmt.Sprint(port))
			start := time.Now()
			if err := tcpReachable(server, port, 8*time.Second); err != nil {
				verdict(diagnose.Verdict{
					Status:  diagnose.Fail,
					Summary: fmt.Sprintf("TCP %s 不可达", addr),
					Explain: "连接被拒绝或超时。三个地方都要放行，漏一个的症状都是超时：" +
						"云控制台安全组、服务器防火墙、服务是否监听在 0.0.0.0。" +
						"这一步不通时，后面所有层都不用看了。",
					Fix: []string{
						"# 服务端执行，确认在监听",
						fmt.Sprintf("ss -lntp | grep %d", port),
						"# 看是否有防火墙拦截",
						"firewall-cmd --list-ports   # 或 ufw status",
						"# 云控制台安全组需手工放行（工具无法代为操作）",
					},
				})
				failed++
			} else {
				detail("TCP %s 可达，耗时 %d ms", addr, time.Since(start).Milliseconds())
			}
		}
	}

	// 后面几层都需要本地代理可用，这里先定位本地端口
	localProxy := "http://127.0.0.1:7890"
	if role == "client" {
		if st := systemctlState(svcName); st != "active" {
			layerHeader(5, "鉴权 / 转发 / 性能")
			verdict(diagnose.Verdict{
				Status:  diagnose.Skip,
				Summary: "跳过：本地中转未运行，无法测试经代理的请求",
			})
			printDoctorFooter(failed)
			return nil
		}
	}

	// ── L5 鉴权 ─────────────────────────────────────────────────
	layerHeader(5, "鉴权")
	authCode := probeViaProxy(localProxy, "https://api.ipify.org")
	v := diagnose.ClassifyProxyResult(authCode)
	verdict(v)
	if v.Status == diagnose.Fail {
		failed++
		printDoctorFooter(failed)
		return nil
	}

	// ── L6 转发（域名特异性）──────────────────────────────────────
	layerHeader(6, "转发")
	// 这三个域名是精心选的：github 通常通，google/chatgpt 是被识别的典型目标。
	// 只测一个域名无法区分「整体故障」和「域名被针对性阻断」。
	targets := []string{
		"https://github.com",
		"https://www.google.com",
		"https://chatgpt.com",
	}
	results := make([]diagnose.DomainResult, 0, len(targets))
	for _, t := range targets {
		code := probeViaProxy(localProxy, t)
		host := strings.TrimPrefix(strings.TrimPrefix(t, "https://"), "www.")
		results = append(results, diagnose.DomainResult{Host: host, Code: code})
		detail("%-14s HTTP %s", host, code)
	}
	v = diagnose.AnalyzeDomainSpecific(results)
	verdict(v)
	if v.Status == diagnose.Fail {
		failed++
	}

	// ── L7 性能 ─────────────────────────────────────────────────
	layerHeader(7, "性能")
	if !o.full {
		verdict(diagnose.Verdict{
			Status:  diagnose.Skip,
			Summary: "跳过（约消耗 10MB 流量），需要时加 --full",
		})
	} else {
		speed := measureThroughput(localProxy)
		cc := kernelCongestionControl()
		detail("实测 %.0f KB/s，拥塞算法 %s", speed/1024, cc)
		v = diagnose.AnalyzeThroughput(diagnose.PerfInput{
			BytesPerSecond:    speed,
			CongestionControl: cc,
			// 丢包与 RTT 需要 ping，这里保守填 0，避免误判；
			// 具体数值由 doctor 的提示引导用户自己测。
		})
		verdict(v)
		if v.Status == diagnose.Fail {
			failed++
		}
	}

	printDoctorFooter(failed)
	return nil
}

// ── 输出辅助 ────────────────────────────────────────────────────

func layerHeader(n int, name string) {
	fmt.Printf("\nL%d %s\n", n, name)
}

func verdict(v diagnose.Verdict) {
	detail("%s %s", v.Status.Symbol(), v.Summary)
	if v.Explain != "" {
		for _, line := range wrapText(v.Explain, 72) {
			detail("       %s", line)
		}
	}
	if len(v.Fix) > 0 {
		detail("")
		for _, cmd := range v.Fix {
			detail("     %s", cmd)
		}
	}
}

func printDoctorFooter(failed int) {
	fmt.Println()
	if failed == 0 {
		fmt.Println("结论：未发现问题。")
		return
	}
	fmt.Printf("结论：%d 层存在问题，从上面第一个 [fail] 开始处理。\n", failed)
	fmt.Println("后面的层失败往往是前面层的连带结果，不要跳着排查。")
}

// wrapText 按宽度折行，避免长解释在一行里糊成一片。
func wrapText(s string, width int) []string {
	var out []string
	var cur strings.Builder
	for _, r := range s {
		if cur.Len() >= width {
			out = append(out, cur.String())
			cur.Reset()
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// ── 探测实现 ────────────────────────────────────────────────────

// probeViaProxy 经代理访问目标，返回 HTTP 状态码字符串，失败返回 "000"。
func probeViaProxy(proxyURL, target string) string {
	pu, err := url.Parse(proxyURL)
	if err != nil {
		return "000"
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
		return "000"
	}
	defer resp.Body.Close()
	return fmt.Sprint(resp.StatusCode)
}

// measureThroughput 经本地代理下载 10MB 测速。
func measureThroughput(proxyURL string) float64 {
	pu, err := url.Parse(proxyURL)
	if err != nil {
		return 0
	}
	client := &http.Client{
		Timeout: 90 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(pu),
		},
	}
	start := time.Now()
	resp, err := client.Get("https://speed.cloudflare.com/__down?bytes=10000000")
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	var total int64
	buf := make([]byte, 32*1024)
	for {
		n, err := resp.Body.Read(buf)
		total += int64(n)
		if err != nil {
			break
		}
	}
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return float64(total) / elapsed
}

// systemctlState 返回服务状态，systemctl 不可用时返回 "unknown"。
func systemctlState(name string) string {
	if !shellHas("systemctl") {
		return "unknown"
	}
	out, err := exec.Command("systemctl", "is-active", name+".service").Output()
	state := strings.TrimSpace(string(out))
	if err != nil && state == "" {
		return "inactive"
	}
	return state
}

// kernelCongestionControl 读取当前拥塞控制算法。
func kernelCongestionControl() string {
	data, err := os.ReadFile("/proc/sys/net/ipv4/tcp_congestion_control")
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(data))
}

// backupList 列出 dukou 做过的配置备份，供 doctor 提示可回滚点。
func backupList() []string {
	entries, err := os.ReadDir(manifestBackupDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, filepath.Join(manifestBackupDir(), e.Name()))
	}
	return out
}

func manifestBackupDir() string {
	return filepath.Join(filepath.Dir(config.DefaultPath()), "backup")
}
