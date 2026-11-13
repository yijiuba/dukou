package manifest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddIsIdempotent(t *testing.T) {
	m := New("server")
	e := Entry{Kind: KindFile, Path: "/etc/dukou.conf", Detail: "配置"}

	m.Add(e)
	m.Add(e)
	m.Add(Entry{Kind: KindFile, Path: "/etc/dukou.conf", Detail: "配置（更新）"})

	if len(m.Entries) != 1 {
		t.Fatalf("重复记录应合并为 1 条，实际 %d 条", len(m.Entries))
	}
	if m.Entries[0].Detail != "配置（更新）" {
		t.Errorf("后写入的应覆盖前者，实际 %q", m.Entries[0].Detail)
	}
}

func TestAddTreatsDifferentKindsSeparately(t *testing.T) {
	m := New("server")
	m.Add(Entry{Kind: KindFile, Path: "/usr/local/bin/gost"})
	m.Add(Entry{Kind: KindBinary, Path: "/usr/local/bin/gost"})

	if len(m.Entries) != 2 {
		t.Fatalf("不同 Kind 应各占一条，实际 %d 条", len(m.Entries))
	}
}

func TestHasAndByKind(t *testing.T) {
	m := New("client")
	m.Add(Entry{Kind: KindService, Path: "dukou.service"})
	m.Add(Entry{Kind: KindFirewall, Path: "firewalld:8443/tcp", Undo: "firewall-cmd ..."})
	m.Add(Entry{Kind: KindFile, Path: "/etc/dukou.conf"})

	if !m.Has(KindService, "dukou.service") {
		t.Error("Has 应找到已记录的服务")
	}
	if m.Has(KindService, "other.service") {
		t.Error("Has 不应找到未记录的对象")
	}
	if got := len(m.ByKind(KindFirewall)); got != 1 {
		t.Errorf("防火墙记录应为 1 条，实际 %d", got)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	m := New("server")
	m.Add(Entry{
		Kind:       KindFile,
		Path:       "/etc/dukou.conf",
		Detail:     "配置文件",
		Backup:     "/var/lib/dukou/backup/etc/dukou.conf.20261113",
		WasPresent: true,
	})
	m.Add(Entry{Kind: KindBinary, Path: "/usr/local/bin/gost", WasPresent: false})

	if err := Save(path, m); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if got.Role != "server" {
		t.Errorf("Role 不对: %q", got.Role)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("Entries 数量应为 2，实际 %d", len(got.Entries))
	}
	if !got.Entries[0].WasPresent {
		t.Error("WasPresent 未保留")
	}
	if got.Entries[0].Backup == "" {
		t.Error("Backup 路径未保留")
	}
	if got.Entries[1].WasPresent {
		t.Error("第二条的 WasPresent 应为 false")
	}
}

func TestLoadMissingFileReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.json")
	m, err := Load(path)
	if err != nil {
		t.Fatalf("文件不存在时不应报错: %v", err)
	}
	if len(m.Entries) != 0 {
		t.Errorf("应为空清单，实际 %d 条", len(m.Entries))
	}
	if m.Version != CurrentVersion {
		t.Errorf("版本应为 %d，实际 %d", CurrentVersion, m.Version)
	}
}

func TestLoadRejectsCorrupted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("损坏的清单应报错")
	}
}

func TestLoadRejectsNewerVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"entries":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("版本高于支持范围时应报错，提示用户升级")
	}
}

func TestSaveOverwriteLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	m := New("server")
	if err := Save(path, m); err != nil {
		t.Fatal(err)
	}
	m.Add(Entry{Kind: KindFile, Path: "/x"})
	if err := Save(path, m); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("残留临时文件: %s", e.Name())
		}
	}
}
