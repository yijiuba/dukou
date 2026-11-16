package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yijiuba/dukou/internal/store"
)

func TestServiceBackupRoundtrip(t *testing.T) {
	src, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv, err := src.AddServer(store.ServerInput{
		Name: "东京 · Vultr", Flag: "JP", Host: "203.0.113.9",
		SSHPassword: "ssh-pw", ProxyPassword: "proxy-pw",
	})
	if err != nil {
		t.Fatalf("AddServer: %v", err)
	}

	path := filepath.Join(t.TempDir(), "cfg") // 故意不带后缀
	sum, err := src.ExportBackup(path, "backup-pass")
	if err != nil {
		t.Fatalf("ExportBackup: %v", err)
	}
	if !strings.HasSuffix(sum.Path, ".dukoubak") {
		t.Fatalf("应自动补 .dukoubak 后缀，实际 %q", sum.Path)
	}
	if sum.Servers != 1 {
		t.Fatalf("导出服务器数 = %d", sum.Servers)
	}

	dst, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := dst.ImportBackup(sum.Path, "backup-pass")
	if err != nil {
		t.Fatalf("ImportBackup: %v", err)
	}
	if got.Added != 1 {
		t.Fatalf("导入统计: %+v", got)
	}
	list, err := dst.ListServers()
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(list) != 1 || list[0].ID != srv.ID {
		t.Fatalf("导入后清单不对: %+v", list)
	}
	_, proxyPw, err := dst.store.ProxyCredentials(srv.ID)
	if err != nil || proxyPw != "proxy-pw" {
		t.Fatalf("代理密码未恢复: %q err=%v", proxyPw, err)
	}
}

func TestServiceBackupRequiresPassword(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExportBackup(filepath.Join(t.TempDir(), "x.dukoubak"), ""); err == nil {
		t.Fatal("空口令必须被拒绝")
	}
}

func TestServiceExportServersCSV(t *testing.T) {
	svc, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddServer(store.ServerInput{Name: "新加坡", Host: "47.84.33.255", SSHPassword: "ssh-pw"}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "list")
	n, err := svc.ExportServers(out, "csv")
	if err != nil {
		t.Fatalf("ExportServers: %v", err)
	}
	if n != 1 {
		t.Fatalf("导出条数 = %d", n)
	}
	data, err := os.ReadFile(out + ".csv")
	if err != nil {
		t.Fatalf("导出的 CSV 不存在: %v", err)
	}
	if !strings.Contains(string(data), "47.84.33.255") {
		t.Fatal("CSV 里没有服务器地址")
	}
	if strings.Contains(string(data), "ssh-pw") {
		t.Fatal("CSV 里出现了明文密码")
	}
}
