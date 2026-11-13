package remote

import (
	"strings"
	"testing"
)

// TestFamilyOf 覆盖真实会遇到的发行版。
// 这张表是整个项目里唯一按发行版维护的地方，错一个就会选错安装通道。
func TestFamilyOf(t *testing.T) {
	cases := []struct {
		id, like, want string
	}{
		{"ubuntu", "debian", "debian"},
		{"debian", "", "debian"},
		{"linuxmint", "ubuntu", "debian"},
		{"deepin", "debian", "debian"},
		{"centos", "rhel fedora", "rhel"},
		{"almalinux", "rhel", "rhel"},
		{"rocky", "rhel", "rhel"},
		{"alinux", "", "rhel"},
		{"anolis", "rhel", "rhel"},
		{"openeuler", "rhel", "rhel"},
		{"kylin", "rhel", "rhel"},
		{"alpine", "", "alpine"},
		{"arch", "", "arch"},
		{"opensuse-leap", "suse", "suse"},
		{"gentoo", "", "unknown"},
		{"", "", "unknown"},
	}
	for _, c := range cases {
		if got := familyOf(c.id, c.like); got != c.want {
			t.Errorf("familyOf(%q,%q)=%q，期望 %q", c.id, c.like, got, c.want)
		}
	}
}

// TestArchOf 确认 uname -m 的常见取值都能翻译成 gost 资产名。
func TestArchOf(t *testing.T) {
	cases := map[string]string{
		"x86_64":  "amd64",
		"aarch64": "arm64",
		"i686":    "386",
		"x86":     "386",
		"armv7l":  "arm",
		"mips":    "",
		"":        "",
	}
	for in, want := range cases {
		if got := archOf(in); got != want {
			t.Errorf("archOf(%q)=%q，期望 %q", in, got, want)
		}
	}
}

// TestParseKV 确认 os-release 的解析能处理引号、注释和空行。
func TestParseKV(t *testing.T) {
	in := `# 注释行
NAME="Ubuntu"
ID=ubuntu
ID_LIKE=debian
PRETTY_NAME="Ubuntu 24.04.1 LTS"

EMPTY=
`
	m := parseKV(in)
	if m["ID"] != "ubuntu" {
		t.Errorf("ID=%q", m["ID"])
	}
	if m["NAME"] != "Ubuntu" {
		t.Errorf("NAME 的引号没去掉: %q", m["NAME"])
	}
	if m["PRETTY_NAME"] != "Ubuntu 24.04.1 LTS" {
		t.Errorf("PRETTY_NAME=%q", m["PRETTY_NAME"])
	}
	if _, ok := m["# 注释行"]; ok {
		t.Error("注释行不应被解析")
	}
}

// TestOSInfoLines 确认摘要里能看出身份与 init 系统，
// 这两项是后续判断"能不能装 systemd 服务"的依据。
func TestOSInfoLines(t *testing.T) {
	i := OSInfo{Pretty: "Ubuntu 24.04", Arch: "amd64", IsRoot: true, Systemd: true}
	lines := i.Lines()
	if len(lines) != 3 {
		t.Fatalf("期望 3 行，实际 %d", len(lines))
	}
	joined := lines[0] + lines[1] + lines[2]
	for _, want := range []string{"Ubuntu 24.04", "amd64", "root", "systemd"} {
		if !strings.Contains(joined, want) {
			t.Errorf("摘要里缺少 %q：%s", want, joined)
		}
	}
}
