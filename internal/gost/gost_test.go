package gost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChannelFor(t *testing.T) {
	cases := []struct {
		family   string
		wantKind string
		wantExt  string
		wantDir  string
	}{
		{"debian", "deb", ".deb", "/usr/bin"},
		{"rhel", "rpm", ".rpm", "/usr/bin"},
		{"suse", "tarball", ".tar.gz", "/usr/local/bin"},
		{"arch", "tarball", ".tar.gz", "/usr/local/bin"},
		{"alpine", "tarball", ".tar.gz", "/usr/local/bin"},
		{"unknown", "tarball", ".tar.gz", "/usr/local/bin"},
	}
	for _, tc := range cases {
		t.Run(tc.family, func(t *testing.T) {
			c := ChannelFor(tc.family)
			if c.Kind != tc.wantKind {
				t.Errorf("Kind 应为 %s，实际 %s", tc.wantKind, c.Kind)
			}
			if c.Ext != tc.wantExt {
				t.Errorf("Ext 应为 %s，实际 %s", tc.wantExt, c.Ext)
			}
			if c.InstallDir != tc.wantDir {
				t.Errorf("InstallDir 应为 %s，实际 %s", tc.wantDir, c.InstallDir)
			}
		})
	}
}

func TestChannelBinPath(t *testing.T) {
	c := Channel{InstallDir: "/usr/local/bin"}
	if got := c.BinPath(); got != "/usr/local/bin/gost" {
		t.Errorf("BinPath 错误: %s", got)
	}
}

func TestAssetName(t *testing.T) {
	// 这些文件名是实测确认过的真实资产，改动会导致 404
	cases := []struct {
		arch string
		ext  string
		want string
	}{
		{"amd64", ".tar.gz", "gost_3.3.3_linux_amd64.tar.gz"},
		{"amd64", ".deb", "gost_3.3.3_linux_amd64.deb"},
		{"amd64", ".rpm", "gost_3.3.3_linux_amd64.rpm"},
		{"arm64", ".tar.gz", "gost_3.3.3_linux_arm64.tar.gz"},
	}
	for _, tc := range cases {
		got, err := AssetName("3.3.3", tc.arch, tc.ext)
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.arch, tc.ext, err)
		}
		if got != tc.want {
			t.Errorf("应为 %s，实际 %s", tc.want, got)
		}
	}
}

func TestAssetNameRejectsUnknownArch(t *testing.T) {
	if _, err := AssetName("3.3.3", "sparc", ".tar.gz"); err == nil {
		t.Error("未知架构应报错")
	}
}

func TestDownloadURL(t *testing.T) {
	got, err := DownloadURL("3.3.3", "amd64", ".tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://github.com/go-gost/gost/releases/download/v3.3.3/gost_3.3.3_linux_amd64.tar.gz"
	if got != want {
		t.Errorf("URL 错误:\n  got  = %s\n  want = %s", got, want)
	}
}

func TestVerifyAsset(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, 1<<20+1024) // 超过 1MB 阈值

	t.Run("合法 gzip", func(t *testing.T) {
		data := append([]byte{0x1f, 0x8b}, big...)
		p := filepath.Join(dir, "a.tar.gz")
		os.WriteFile(p, data, 0o644)
		if err := verifyAsset(p); err != nil {
			t.Errorf("合法 gzip 应通过: %v", err)
		}
	})

	t.Run("HTML 错误页冒充 gzip", func(t *testing.T) {
		data := append([]byte("<html>404"), big...)
		p := filepath.Join(dir, "b.tar.gz")
		os.WriteFile(p, data, 0o644)
		if err := verifyAsset(p); err == nil {
			t.Error("HTML 冒充 gzip 应被拒绝")
		}
	})

	t.Run("合法 deb", func(t *testing.T) {
		data := append([]byte("!<arch>\n"), big...)
		p := filepath.Join(dir, "c.deb")
		os.WriteFile(p, data, 0o644)
		if err := verifyAsset(p); err != nil {
			t.Errorf("合法 deb 应通过: %v", err)
		}
	})

	t.Run("合法 rpm", func(t *testing.T) {
		data := append([]byte{0xed, 0xab, 0xee, 0xdb}, big...)
		p := filepath.Join(dir, "d.rpm")
		os.WriteFile(p, data, 0o644)
		if err := verifyAsset(p); err != nil {
			t.Errorf("合法 rpm 应通过: %v", err)
		}
	})

	t.Run("文件过小", func(t *testing.T) {
		p := filepath.Join(dir, "e.tar.gz")
		os.WriteFile(p, []byte{0x1f, 0x8b, 0x00}, 0o644)
		err := verifyAsset(p)
		if err == nil {
			t.Fatal("过小的文件应被拒绝")
		}
		if !strings.Contains(err.Error(), "不完整") {
			t.Errorf("错误信息应说明不完整，实际: %v", err)
		}
	})
}

func TestSHA256(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	os.WriteFile(p, []byte("hello"), 0o644)

	got, err := SHA256(p)
	if err != nil {
		t.Fatal(err)
	}
	// sha256("hello")
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Errorf("摘要错误:\n  got  = %s\n  want = %s", got, want)
	}
}

func TestDetectInstalledVersionMissingBinary(t *testing.T) {
	if v := DetectInstalledVersion("/nonexistent/gost"); v != "" {
		t.Errorf("不存在的二进制应返回空串，实际 %q", v)
	}
}
