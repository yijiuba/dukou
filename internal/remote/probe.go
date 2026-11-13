package remote

import (
	"fmt"
	"strings"
)

// OSInfo 是对目标机器的一次快照，后续每一步都基于它做决策。
type OSInfo struct {
	Pretty  string // 例如 "Ubuntu 24.04.1 LTS"
	ID      string // 例如 "ubuntu"
	Family  string // debian / rhel / alpine / arch / suse / unknown
	Arch    string // gost 资产名里的架构段：amd64 / arm64 / 386 / arm
	IsRoot  bool
	Systemd bool
	HasCurl bool
}

// Probe 在目标机器上收集系统信息。
//
// 只用 POSIX 基础命令，不依赖 lsb_release 之类的可选项——
// 最小化的容器镜像里这些东西经常不存在。
func Probe(e Exec) (OSInfo, error) {
	var info OSInfo

	out, err := e.Run("cat /etc/os-release 2>/dev/null || cat /usr/lib/os-release 2>/dev/null")
	if err != nil && strings.TrimSpace(out) == "" {
		return info, fmt.Errorf("读不到 /etc/os-release，这可能不是 Linux 系统")
	}
	fields := parseKV(out)
	info.ID = strings.ToLower(fields["ID"])
	info.Pretty = fields["PRETTY_NAME"]
	if info.Pretty == "" {
		info.Pretty = info.ID
	}
	info.Family = familyOf(info.ID, fields["ID_LIKE"])

	arch, err := e.Run("uname -m")
	if err != nil {
		return info, fmt.Errorf("执行 uname 失败: %w", err)
	}
	info.Arch = archOf(strings.TrimSpace(arch))
	if info.Arch == "" {
		return info, fmt.Errorf("不支持的 CPU 架构 %q", strings.TrimSpace(arch))
	}

	if uid, err := e.Run("id -u"); err == nil {
		info.IsRoot = strings.TrimSpace(uid) == "0"
	}

	// systemctl 存在不代表能用（容器里常常存在但没起 PID 1）
	if out, err := e.Run("command -v systemctl >/dev/null 2>&1 && echo yes || echo no"); err == nil {
		info.Systemd = strings.TrimSpace(out) == "yes"
	}
	if out, err := e.Run("command -v curl >/dev/null 2>&1 && echo yes || echo no"); err == nil {
		info.HasCurl = strings.TrimSpace(out) == "yes"
	}

	return info, nil
}

// Lines 生成给人看的几行摘要。
func (i OSInfo) Lines() []string {
	root := "普通用户"
	if i.IsRoot {
		root = "root"
	}
	init := "非 systemd"
	if i.Systemd {
		init = "systemd"
	}
	return []string{
		fmt.Sprintf("系统  %s", i.Pretty),
		fmt.Sprintf("架构  %s", i.Arch),
		fmt.Sprintf("身份  %s，%s", root, init),
	}
}

// familyOf 把发行版 ID 归并成安装通道需要的几个家族。
// 这是整个项目里唯一需要逐发行版维护的表：新增发行版只改这里。
func familyOf(id, idLike string) string {
	hay := id + " " + idLike
	switch {
	case strings.Contains(hay, "debian"), strings.Contains(hay, "ubuntu"),
		strings.Contains(hay, "raspbian"), strings.Contains(hay, "linuxmint"),
		strings.Contains(hay, "deepin"), strings.Contains(hay, "kali"):
		return "debian"
	case strings.Contains(hay, "rhel"), strings.Contains(hay, "fedora"),
		strings.Contains(hay, "centos"), strings.Contains(hay, "almalinux"),
		strings.Contains(hay, "rocky"), strings.Contains(hay, "openeuler"),
		strings.Contains(hay, "anolis"), strings.Contains(hay, "alinux"),
		strings.Contains(hay, "kylin"):
		return "rhel"
	case strings.Contains(hay, "alpine"):
		return "alpine"
	case strings.Contains(hay, "arch"), strings.Contains(hay, "manjaro"):
		return "arch"
	case strings.Contains(hay, "suse"), strings.Contains(hay, "opensuse"):
		return "suse"
	}
	return "unknown"
}

// archOf 把 uname -m 的结果翻译成 gost 资产名用的架构段。
func archOf(m string) string {
	switch m {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	case "i386", "i686", "x86":
		return "386"
	case "armv7l", "armv6l", "arm":
		return "arm"
	}
	return ""
}

// parseKV 解析 KEY=VALUE 格式的文本，自动去掉值两端的引号。
func parseKV(s string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return m
}
