package cmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yijiuba/dukou/internal/config"
	"github.com/yijiuba/dukou/internal/detect"
	"github.com/yijiuba/dukou/internal/firewall"
	"github.com/yijiuba/dukou/internal/gost"
	"github.com/yijiuba/dukou/internal/manifest"
	"github.com/yijiuba/dukou/internal/shell"
	"github.com/yijiuba/dukou/internal/systemd"
)

type serverOptions struct {
	port       int
	user       string
	password   string
	version    string
	dryRun     bool
	takeover   bool
	skipFW     bool
	confPath   string
	reinstall  bool
	downloadTo string
}

func runServer(args []string) error {
	var o serverOptions
	fs := flag.NewFlagSet("dukou server", flag.ContinueOnError)
	fs.IntVar(&o.port, "port", 8443, "TLS 加密代理端口，对外只开这一个")
	fs.StringVar(&o.user, "user", "dukou", "代理用户名")
	fs.StringVar(&o.password, "password", "", "代理密码，留空则自动生成 16 位随机串")
	fs.StringVar(&o.version, "gost-version", gost.DefaultVersion, "gost 版本")
	fs.BoolVar(&o.dryRun, "dry-run", false, "只打印执行计划，不改动系统")
	fs.BoolVar(&o.takeover, "takeover", false, "授权接管被占用的端口")
	fs.BoolVar(&o.skipFW, "skip-firewall", false, "跳过防火墙配置（安全组手动放行时使用）")
	fs.StringVar(&o.confPath, "conf", config.DefaultPath(), "配置文件路径")
	fs.BoolVar(&o.reinstall, "reinstall", false, "已安装时强制重装 gost")
	fs.StringVar(&o.downloadTo, "download-dir", "/tmp/dukou", "安装包临时下载目录")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fmt.Println("dukou server ─ 部署代理服务端")
	const total = 8

	var runner *shell.Runner
	if o.dryRun {
		runner = shell.Dry(os.Stdout)
	} else {
		runner = shell.New(os.Stdout)
	}

	// ── 1 环境探测 ───────────────────────────────────────────────
	step(1, total, "环境探测")
	profile := detect.Detect(detect.NewEnv())
	for _, line := range profile.Lines() {
		detail("%s", line)
	}
	if !o.dryRun {
		if !profile.IsRoot {
			return fmt.Errorf("部署服务端需要 root：请用 sudo 重新执行")
		}
		if profile.Init != detect.Systemd {
			return fmt.Errorf("当前初始化系统为 %s，v0.1 只支持 systemd", profile.Init)
		}
	}
	channel := gost.ChannelFor(string(profile.Family))
	detail("gost 安装通道：%s（%s 家族，架构 %s）", channel.Kind, profile.Family, profile.Arch)

	// ── 2 端口检查 ───────────────────────────────────────────────
	step(2, total, "端口检查")
	portFree := checkPortFree(o.port) == nil
	if portFree {
		detail(":%d 空闲", o.port)
	} else {
		holder := portHolder(o.port)
		if holder != "" {
			detail(":%d 已被占用，占用者：%s", o.port, holder)
		} else {
			detail(":%d 已被占用", o.port)
		}
		if !o.takeover {
			return fmt.Errorf("端口 %d 被占用。若确认可以接管（例如停掉旧的代理进程），"+
				"加 --takeover 重新执行；或换一个端口 --port 9443", o.port)
		}
		detail("已按 --takeover 授权接管")
	}

	// ── 3 准备配置 ───────────────────────────────────────────────
	step(3, total, "准备配置")
	password := o.password
	if password == "" {
		existing := loadExistingConfig(o.confPath)
		if existing.Password != "" && existing.Port == o.port {
			password = existing.Password
			detail("复用 %s 中已有的密码", o.confPath)
		} else {
			gen, err := config.GeneratePassword(16)
			if err != nil {
				return err
			}
			password = gen
			detail("已生成 16 位随机密码")
		}
	}
	if len(password) < 8 {
		return fmt.Errorf("密码长度不足 8 位，公网代理容易被扫描爆破")
	}
	cfg := config.Config{
		Role:     config.RoleServer,
		Port:     o.port,
		User:     o.user,
		Password: password,
		Created:  time.Now().Format(time.RFC3339),
	}
	detail("角色 %s，端口 %d，用户名 %s", cfg.Role, cfg.Port, cfg.User)

	// ── 4 安装 gost ──────────────────────────────────────────────
	step(4, total, "安装 gost")
	binPath := channel.BinPath()
	installed := installedVersion(binPath)
	switch {
	case installed == o.version && !o.reinstall:
		detail("%s 已安装 v%s，跳过", binPath, installed)
	case installed != "" && !o.reinstall:
		detail("已装 v%s，与目标 v%s 不同；加 --reinstall 可强制替换",
			installed, o.version)
		binPath = "gost"
	case o.dryRun:
		url, err := gost.DownloadURL(o.version, profile.Arch, channel.Ext)
		if err != nil {
			return err
		}
		detail("将下载 %s", url)
		detail("安装到 %s", channel.BinPath())
	default:
		got, err := installGost(o, profile, channel)
		if err != nil {
			return err
		}
		binPath = got
	}

	// ── 5 写配置 ─────────────────────────────────────────────────
	step(5, total, "写入配置")
	if o.dryRun {
		detail("将写入 %s（权限 0600，含密码）", o.confPath)
		detail("内容：ROLE=server PORT=%d USER=%s PASSWORD=<16位随机>", o.port, o.user)
	} else {
		if err := writeConfigWithBackup(o.confPath, cfg); err != nil {
			return err
		}
		detail("已写入 %s", o.confPath)
	}

	// ── 6 注册 systemd ───────────────────────────────────────────
	step(6, total, "注册 systemd 服务")
	unit := systemd.ServerUnit(binPath, o.port, o.user, password)
	if o.dryRun {
		detail("将写入 %s", unit.Path())
		detail("ExecStart=%s", unit.ExecStart)
		for _, c := range renderCmds(unit.EnableCommands()) {
			detail("  $ %s", c)
		}
	} else {
		if err := installUnit(runner, unit); err != nil {
			return err
		}
		detail("已启用 %s.service（Restart=always，开机自启）", unit.Name)
	}

	// ── 7 防火墙 ─────────────────────────────────────────────────
	step(7, total, "防火墙")
	rule := firewall.Rule{Port: o.port, Proto: "tcp"}
	switch {
	case o.skipFW:
		detail("已按 --skip-firewall 跳过；请自行确认 %s 已放行", rule)
	case profile.Firewall == detect.NoFirewall:
		detail("未检测到生效的防火墙，跳过")
	default:
		cmds := firewall.Plan(string(profile.Firewall), rule)
		if len(cmds) == 0 {
			detail("%s", firewall.ManualHint(string(profile.Firewall), rule))
		} else {
			for _, c := range cmds {
				detail("  $ %s", strings.Join(c, " "))
				if err := runner.Run(c[0], c[1:]...); err != nil {
					return fmt.Errorf("防火墙配置失败（可加 --skip-firewall 手动处理）: %w", err)
				}
			}
			detail("%s 已放行 %s", profile.Firewall, rule)
		}
	}
	for _, line := range firewall.CloudHint(o.port) {
		detail("%s", line)
	}

	// ── 8 验证门 ─────────────────────────────────────────────────
	step(8, total, "验证门")
	if o.dryRun {
		detail("将执行：curl -k -o /dev/null -w '%%{http_code}' https://127.0.0.1:%d", o.port)
		detail("期望返回 407（TLS 与代理层均正常，仅缺凭据）")
		fmt.Println("\n--dry-run：以上为完整计划，未改动系统。")
		return nil
	}

	code, err := probeLocalTLS(o.port)
	if err != nil {
		return fmt.Errorf("验证失败，服务可能未启动：%w\n"+
			"      排查：systemctl status %s / journalctl -u %s -n 30", err, unit.Name, unit.Name)
	}
	if code != "407" {
		return fmt.Errorf("验证失败：期望 HTTP 407（代理要求鉴权），实际 %s\n"+
			"      407 表示 TLS 握手与代理层都正常；其它值说明服务未按预期工作\n"+
			"      排查：journalctl -u %s -n 30", code, unit.Name)
	}
	detail("HTTP %s ✓ TLS 握手与代理层正常", code)

	// 记录清单
	if err := saveManifest("server", unit.Path(), o.confPath, rule,
		string(profile.Firewall), channel, o.port); err != nil {
		detail("警告：清单写入失败，uninstall 将无法精确回滚：%v", err)
	}

	printServerSummary(o, cfg, unit)
	return nil
}

// ── 辅助函数 ────────────────────────────────────────────────────

func loadExistingConfig(path string) config.Config {
	c, err := config.Load(path)
	if err != nil {
		return config.Config{}
	}
	return c
}

// installedVersion 返回已安装 gost 的版本号，未安装返回空串。
func installedVersion(binPath string) string {
	if v := gost.DetectInstalledVersion(binPath); v != "" {
		return v
	}
	// deb/rpm 与二进制安装位置可能不同，再从 PATH 里找一次
	if p, err := lookPath("gost"); err == nil {
		return gost.DetectInstalledVersion(p)
	}
	return ""
}

// installGost 按通道下载并安装，返回最终二进制路径。
func installGost(o serverOptions, profile detect.Profile, channel gost.Channel) (string, error) {
	url, err := gost.DownloadURL(o.version, profile.Arch, channel.Ext)
	if err != nil {
		return "", err
	}
	detail("下载 %s", url)

	path, size, err := gost.Fetch(url, o.downloadTo)
	if err != nil {
		return "", err
	}
	detail("已下载 %.1f MB 到 %s", float64(size)/(1<<20), path)

	sum, err := gost.SHA256(path)
	if err == nil {
		detail("SHA256 %s", sum)
	}

	switch channel.Kind {
	case "deb":
		if err := shell.New(os.Stdout).Run("dpkg", "-i", path); err != nil {
			return "", fmt.Errorf("dpkg 安装失败（可能缺少依赖，可尝试 apt -f install）: %w", err)
		}
	case "rpm":
		if err := shell.New(os.Stdout).Run("dnf", "install", "-y", path); err != nil {
			// dnf 不可用时退回 rpm
			if err2 := shell.New(os.Stdout).Run("rpm", "-Uvh", path); err2 != nil {
				return "", fmt.Errorf("rpm 安装失败: %w", err)
			}
		}
	default:
		binPath, err := extractTarball(path, channel.InstallDir)
		if err != nil {
			return "", err
		}
		detail("已解压到 %s", binPath)
		return binPath, nil
	}

	// 包管理器装完后确认实际路径
	if p, err := lookPath("gost"); err == nil {
		return p, nil
	}
	return channel.BinPath(), nil
}

func writeConfigWithBackup(path string, c config.Config) error {
	// 已存在的配置先备份，让用户随时能回到原状
	if _, err := os.Stat(path); err == nil {
		bak := filepath.Join(manifest.BackupDir(),
			filepath.Base(path)+"."+time.Now().Format("20060102-150405"))
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := config.WriteAtomic(bak, data, 0o600); err != nil {
			return fmt.Errorf("备份原配置失败: %w", err)
		}
	}
	return config.Save(path, c)
}

func installUnit(runner *shell.Runner, u systemd.Unit) error {
	if err := config.WriteAtomic(u.Path(), u.Render(), 0o644); err != nil {
		return err
	}
	for _, c := range u.EnableCommands() {
		if err := runner.Run(c[0], c[1:]...); err != nil {
			return err
		}
	}
	return nil
}

func saveManifest(role, unitPath, confPath string, rule firewall.Rule,
	fw string, channel gost.Channel, port int) error {

	m, err := manifest.Load(manifest.DefaultPath())
	if err != nil {
		m = manifest.New(role)
	}
	if m.Role == "" {
		m.Role = role
	}

	m.Add(manifest.Entry{
		Kind:       manifest.KindFile,
		Path:       confPath,
		Detail:     "dukou 配置文件（含代理密码）",
		WasPresent: true,
	})
	m.Add(manifest.Entry{
		Kind:       manifest.KindService,
		Path:       unitPath,
		Detail:     "systemd 单元",
		WasPresent: false,
	})
	if cmds := firewall.UndoPlan(fw, rule); len(cmds) > 0 {
		m.Add(manifest.Entry{
			Kind:   manifest.KindFirewall,
			Path:   fw + ":" + rule.String(),
			Detail: "放行的入方向端口",
			Undo:   strings.Join(renderCmds(cmds), " && "),
		})
	}
	return manifest.Save(manifest.DefaultPath(), m)
}

func printServerSummary(o serverOptions, cfg config.Config, unit systemd.Unit) {
	fmt.Println()
	fmt.Println("服务端部署完成。")
	fmt.Println()
	fmt.Println("  地址      " + publicAddress() + ":" + fmt.Sprint(o.port))
	fmt.Printf("  用户名    %s\n", cfg.User)
	fmt.Printf("  密码      %s\n", cfg.Password)
	fmt.Printf("  配置      %s\n", o.confPath)
	fmt.Printf("  服务      systemctl status %s\n", unit.Name)
	fmt.Println()
	fmt.Println("在受控机器上执行：")
	fmt.Printf("  dukou client --server %s --port %d --user %s --password '%s'\n",
		publicAddress(), o.port, cfg.User, cfg.Password)
	fmt.Println()
	fmt.Println("密码会出现在 shell 历史里，建议改为交互输入或从文件读取。")
}
