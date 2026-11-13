// Package detect 探测运行环境，把「这台机器长什么样」收敛成一组 profile 变量。
//
// 所有安装、诊断、卸载逻辑只依赖这里产出的 Profile，不自己调用 lsb_release、
// uname、systemctl 之类的命令。这样新增一个发行版只需要改本文件里的映射表，
// 不需要动任何一个子命令的实现。
package detect

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// OSFamily 是包管理器层面的发行版家族。
type OSFamily string

const (
	Debian  OSFamily = "debian"  // apt / dpkg
	RHEL    OSFamily = "rhel"    // dnf / yum / rpm
	SUSE    OSFamily = "suse"    // zypper
	Arch    OSFamily = "arch"    // pacman
	Alpine  OSFamily = "alpine"  // apk
	Unknown OSFamily = "unknown" // 走静态二进制兜底
)

// PkgCmd 返回该家族的原生包管理器命令，Unknown 家族返回空串表示走二进制兜底。
func (f OSFamily) PkgCmd() string {
	switch f {
	case Debian:
		return "apt"
	case RHEL:
		return "dnf"
	case SUSE:
		return "zypper"
	case Arch:
		return "pacman"
	case Alpine:
		return "apk"
	}
	return ""
}

// InitSystem 是 PID 1 实际使用的初始化系统。
type InitSystem string

const (
	Systemd InitSystem = "systemd"
	OpenRC  InitSystem = "openrc"
	NoInit  InitSystem = "none"
)

// Firewall 是系统上实际生效的防火墙前端。
type Firewall string

const (
	Firewalld  Firewall = "firewalld"
	UFW        Firewall = "ufw"
	Nftables   Firewall = "nftables"
	NoFirewall Firewall = "none"
)

// Profile 是一台机器的环境画像，所有分支逻辑都基于它。
type Profile struct {
	Family    OSFamily
	OSID      string
	OSVersion string
	OSPretty  string
	Arch      string
	Init      InitSystem
	Firewall  Firewall
	Desktop   string
	IsRoot    bool
}

// Env 把探测需要的外部世界抽象出来，单元测试可整体替换为假实现。
type Env struct {
	GOARCH   string
	ReadFile func(path string) ([]byte, error)
	LookPath func(file string) (string, error)
	Run      func(name string, args ...string) (string, error)
	Getenv   func(key string) string
	EUID     func() int
}

// NewEnv 返回读取真实系统状态的 Env。
func NewEnv() Env {
	return Env{
		GOARCH:   runtime.GOARCH,
		ReadFile: os.ReadFile,
		LookPath: exec.LookPath,
		Run: func(name string, args ...string) (string, error) {
			out, err := exec.Command(name, args...).Output()
			return string(out), err
		},
		Getenv: os.Getenv,
		EUID:   os.Geteuid,
	}
}

// Detect 产出当前机器的 Profile。
func Detect(env Env) Profile {
	p := Profile{Arch: env.GOARCH}
	p.Family, p.OSID, p.OSVersion, p.OSPretty = detectOS(env)
	p.Init = detectInit(env)
	p.Firewall = detectFirewall(env, p.Init)
	p.Desktop = detectDesktop(env)
	p.IsRoot = env.EUID() == 0
	return p
}

func detectOS(env Env) (OSFamily, string, string, string) {
	raw, err := env.ReadFile("/etc/os-release")
	if err != nil {
		return Unknown, "", "", ""
	}
	kv := parseOSRelease(raw)

	id := strings.ToLower(kv["ID"])
	like := strings.ToLower(kv["ID_LIKE"])

	family := familyOf(id)
	if family == Unknown {
		for _, token := range strings.Fields(like) {
			if fam := familyOf(token); fam != Unknown {
				family = fam
				break
			}
		}
	}
	return family, id, kv["VERSION_ID"], kv["PRETTY_NAME"]
}

// familyOf 是全部发行版差异的唯一集中点：新增发行版只在这里加一行。
func familyOf(id string) OSFamily {
	switch id {
	case "debian", "ubuntu", "linuxmint", "raspbian", "kali", "deepin", "uos", "kylin":
		return Debian
	case "rhel", "centos", "rocky", "almalinux", "fedora", "openeuler", "anolis",
		"opencloudos", "alinux", "alibabacloud":
		return RHEL
	case "opensuse", "opensuse-leap", "opensuse-tumbleweed", "sles", "suse":
		return SUSE
	case "arch", "manjaro", "endeavouros":
		return Arch
	case "alpine":
		return Alpine
	}
	return Unknown
}

func parseOSRelease(raw []byte) map[string]string {
	kv := make(map[string]string, 12)
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		kv[strings.TrimSpace(key)] = val
	}
	return kv
}

func detectInit(env Env) InitSystem {
	if raw, err := env.ReadFile("/proc/1/comm"); err == nil {
		if strings.Contains(strings.ToLower(string(raw)), "systemd") {
			return Systemd
		}
	}
	if _, err := env.LookPath("rc-service"); err == nil {
		return OpenRC
	}
	if _, err := env.LookPath("systemctl"); err == nil {
		return Systemd
	}
	return NoInit
}

func detectFirewall(env Env, init InitSystem) Firewall {
	if init == Systemd {
		if out, err := env.Run("systemctl", "is-active", "firewalld"); err == nil {
			if strings.TrimSpace(out) == "active" {
				return Firewalld
			}
		}
	}
	if _, err := env.LookPath("ufw"); err == nil {
		if out, err := env.Run("ufw", "status"); err == nil {
			if strings.Contains(strings.ToLower(out), "status: active") {
				return UFW
			}
		}
	}
	if _, err := env.LookPath("nft"); err == nil {
		if _, err := env.Run("nft", "list", "ruleset"); err == nil {
			return Nftables
		}
	}
	return NoFirewall
}

func detectDesktop(env Env) string {
	desktop := strings.ToLower(strings.TrimSpace(env.Getenv("XDG_CURRENT_DESKTOP")))
	if desktop == "" {
		if env.Getenv("WAYLAND_DISPLAY") != "" || env.Getenv("DISPLAY") != "" {
			return "unknown-gui"
		}
		return "headless"
	}
	if i := strings.IndexAny(desktop, ":;"); i >= 0 {
		desktop = desktop[:i]
	}
	switch {
	case strings.Contains(desktop, "gnome"), strings.Contains(desktop, "ubuntu"):
		return "gnome"
	case strings.Contains(desktop, "kde"), strings.Contains(desktop, "plasma"):
		return "kde"
	case strings.Contains(desktop, "xfce"):
		return "xfce"
	}
	return desktop
}

// Lines 把 Profile 渲染为 doctor 直接可打印的多行文本。
func (p Profile) Lines() []string {
	osName := p.OSPretty
	if osName == "" {
		osName = "未知发行版"
	}
	desktop := p.Desktop
	if desktop == "" {
		desktop = "headless"
	}
	root := "否（需要 sudo）"
	if p.IsRoot {
		root = "是"
	}
	return []string{
		fmt.Sprintf("发行版家族   %s（ID=%s, 版本=%s）", p.Family, orDash(p.OSID), orDash(p.OSVersion)),
		fmt.Sprintf("系统名称     %s", osName),
		fmt.Sprintf("架构         %s", p.Arch),
		fmt.Sprintf("初始化系统   %s", p.Init),
		fmt.Sprintf("防火墙       %s", p.Firewall),
		fmt.Sprintf("桌面环境     %s", desktop),
		fmt.Sprintf("root 权限    %s", root),
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
