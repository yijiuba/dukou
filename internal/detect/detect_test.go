package detect

import (
	"os"
	"strings"
	"testing"
)

type fakeSystem struct {
	files map[string]string
	bins  map[string]bool
	runs  map[string]string
	envs  map[string]string
	euid  int
	arch  string
}

func (f fakeSystem) env() Env {
	arch := f.arch
	if arch == "" {
		arch = "amd64"
	}
	return Env{
		GOARCH: arch,
		ReadFile: func(path string) ([]byte, error) {
			if content, ok := f.files[path]; ok {
				return []byte(content), nil
			}
			return nil, os.ErrNotExist
		},
		LookPath: func(file string) (string, error) {
			if f.bins[file] {
				return "/usr/bin/" + file, nil
			}
			return "", os.ErrNotExist
		},
		Run: func(name string, args ...string) (string, error) {
			key := name + " " + strings.Join(args, " ")
			if out, ok := f.runs[key]; ok {
				return out, nil
			}
			return "", os.ErrNotExist
		},
		Getenv: func(key string) string { return f.envs[key] },
		EUID:   func() int { return f.euid },
	}
}

func TestDetectUbuntuDesktop(t *testing.T) {
	p := Detect(fakeSystem{
		files: map[string]string{
			"/etc/os-release": `NAME="Ubuntu"
VERSION="24.04.1 LTS (Noble Numbat)"
ID=ubuntu
ID_LIKE=debian
VERSION_ID="24.04"
PRETTY_NAME="Ubuntu 24.04.1 LTS"
`,
			"/proc/1/comm": "systemd\n",
		},
		bins: map[string]bool{"systemctl": true, "ufw": true},
		runs: map[string]string{
			"systemctl is-active firewalld": "inactive\n",
			"ufw status":                    "Status: active\n",
		},
		envs: map[string]string{"XDG_CURRENT_DESKTOP": "ubuntu:GNOME"},
		euid: 1000,
	}.env())

	if p.Family != Debian {
		t.Errorf("Family = %q, 期望 debian", p.Family)
	}
	if p.Init != Systemd {
		t.Errorf("Init = %q, 期望 systemd", p.Init)
	}
	if p.Firewall != UFW {
		t.Errorf("Firewall = %q, 期望 ufw", p.Firewall)
	}
	if p.Desktop != "gnome" {
		t.Errorf("Desktop = %q, 期望 gnome", p.Desktop)
	}
	if p.IsRoot {
		t.Error("IsRoot 应为 false")
	}
	if got := p.Family.PkgCmd(); got != "apt" {
		t.Errorf("PkgCmd = %q, 期望 apt", got)
	}
}

func TestDetectAlibabaCloudLinux(t *testing.T) {
	p := Detect(fakeSystem{
		files: map[string]string{
			"/etc/os-release": `NAME="Alibaba Cloud Linux"
VERSION="3 (Soaring Falcon)"
ID=alinux
ID_LIKE="rhel fedora centos"
VERSION_ID="3"
PRETTY_NAME="Alibaba Cloud Linux 3 (Soaring Falcon)"
`,
			"/proc/1/comm": "systemd\n",
		},
		bins: map[string]bool{"systemctl": true, "nft": true},
		runs: map[string]string{
			"systemctl is-active firewalld": "active\n",
			"nft list ruleset":              "",
		},
		envs: map[string]string{},
		euid: 0,
	}.env())

	if p.Family != RHEL {
		t.Errorf("Family = %q, 期望 rhel", p.Family)
	}
	if p.Firewall != Firewalld {
		t.Errorf("Firewall = %q, 期望 firewalld", p.Firewall)
	}
	if !p.IsRoot {
		t.Error("IsRoot 应为 true")
	}
	if got := p.Family.PkgCmd(); got != "dnf" {
		t.Errorf("PkgCmd = %q, 期望 dnf", got)
	}
}

func TestDetectAlpineOpenRC(t *testing.T) {
	p := Detect(fakeSystem{
		files: map[string]string{
			"/etc/os-release": "ID=alpine\nVERSION_ID=3.20.3\nPRETTY_NAME=\"Alpine Linux v3.20\"\n",
		},
		bins: map[string]bool{"rc-service": true},
		envs: map[string]string{},
		euid: 0,
	}.env())

	if p.Family != Alpine {
		t.Errorf("Family = %q, 期望 alpine", p.Family)
	}
	if p.Init != OpenRC {
		t.Errorf("Init = %q, 期望 openrc", p.Init)
	}
	if p.Firewall != NoFirewall {
		t.Errorf("Firewall = %q, 期望 none", p.Firewall)
	}
	if got := p.Family.PkgCmd(); got != "apk" {
		t.Errorf("PkgCmd = %q, 期望 apk", got)
	}
}

func TestDetectUnknownDistroFallsBackToBinary(t *testing.T) {
	p := Detect(fakeSystem{
		files: map[string]string{
			"/etc/os-release": "ID=voidlinux\nPRETTY_NAME=\"Void Linux\"\n",
		},
		bins: map[string]bool{},
		envs: map[string]string{},
		euid: 0,
	}.env())

	if p.Family != Unknown {
		t.Errorf("Family = %q, 期望 unknown", p.Family)
	}
	if got := p.Family.PkgCmd(); got != "" {
		t.Errorf("PkgCmd = %q, 期望空串（表示走静态二进制兜底）", got)
	}
	if p.Init != NoInit {
		t.Errorf("Init = %q, 期望 none", p.Init)
	}
}

func TestDetectNoOSRelease(t *testing.T) {
	p := Detect(fakeSystem{
		files: map[string]string{},
		bins:  map[string]bool{},
		envs:  map[string]string{},
		euid:  0,
	}.env())

	if p.Family != Unknown {
		t.Errorf("Family = %q, 期望 unknown", p.Family)
	}
	if p.OSPretty != "" {
		t.Errorf("OSPretty = %q, 期望空", p.OSPretty)
	}
}

func TestIDLikeFallback(t *testing.T) {
	p := Detect(fakeSystem{
		files: map[string]string{
			"/etc/os-release": "ID=somelinux\nID_LIKE=\"debian ubuntu\"\n",
		},
		bins: map[string]bool{},
		envs: map[string]string{},
		euid: 0,
	}.env())

	if p.Family != Debian {
		t.Errorf("Family = %q, 期望 debian（由 ID_LIKE 推导）", p.Family)
	}
}

func TestDesktopDetection(t *testing.T) {
	cases := []struct {
		name  string
		envs  map[string]string
		want  string
	}{
		{"gnome", map[string]string{"XDG_CURRENT_DESKTOP": "ubuntu:GNOME"}, "gnome"},
		{"kde", map[string]string{"XDG_CURRENT_DESKTOP": "KDE"}, "kde"},
		{"xfce", map[string]string{"XDG_CURRENT_DESKTOP": "XFCE"}, "xfce"},
		{"headless", map[string]string{}, "headless"},
		{"gui-unknown", map[string]string{"DISPLAY": ":0"}, "unknown-gui"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Detect(fakeSystem{
				files: map[string]string{},
				bins:  map[string]bool{},
				envs:  c.envs,
				euid:  0,
			}.env()).Desktop
			if got != c.want {
				t.Errorf("Desktop = %q, 期望 %q", got, c.want)
			}
		})
	}
}

func TestParseOSRelease(t *testing.T) {
	raw := []byte(`# 注释行应被忽略
NAME="Test Linux"

ID=testos
VERSION_ID='1.2'
PRETTY_NAME="Test Linux 1.2"
NOEQUALS
`)
	kv := parseOSRelease(raw)

	if kv["ID"] != "testos" {
		t.Errorf("ID = %q, 期望 testos", kv["ID"])
	}
	if kv["VERSION_ID"] != "1.2" {
		t.Errorf("VERSION_ID = %q, 期望 1.2（单引号应被剥离）", kv["VERSION_ID"])
	}
	if kv["NAME"] != "Test Linux" {
		t.Errorf("NAME = %q, 期望 Test Linux", kv["NAME"])
	}
	if _, ok := kv["NOEQUALS"]; ok {
		t.Error("没有等号的行不应进入结果")
	}
}

func TestArchArm64(t *testing.T) {
	p := Detect(fakeSystem{
		files: map[string]string{
			"/etc/os-release": "ID=debian\nVERSION_ID=12\n",
		},
		bins: map[string]bool{},
		envs: map[string]string{},
		euid: 0,
		arch: "arm64",
	}.env())

	if p.Arch != "arm64" {
		t.Errorf("Arch = %q, 期望 arm64", p.Arch)
	}
}

func TestLinesNotEmpty(t *testing.T) {
	p := Profile{Family: Debian, Arch: "amd64", Init: Systemd, Firewall: UFW, Desktop: "headless"}
	lines := p.Lines()
	if len(lines) != 7 {
		t.Fatalf("Lines 返回 %d 行, 期望 7 行", len(lines))
	}
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			t.Error("存在空行")
		}
	}
}
