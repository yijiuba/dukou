package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seed 建一个有服务器与凭据的 Store，返回服务器 ID。
func seed(t *testing.T) (*Store, string) {
	t.Helper()
	st := openTestStore(t)
	s, err := st.Add(ServerInput{
		Name: "新加坡 · 腾讯云轻量", Flag: "SG", Host: "47.84.33.255",
		AuthType: "password", SSHPassword: "ssh-secret", ProxyPassword: "proxy-secret",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return st, s.ID
}

func TestBackupRoundtripRestoresCredentials(t *testing.T) {
	src, id := seed(t)
	bak := filepath.Join(t.TempDir(), "cfg.dukoubak")

	sum, err := src.ExportBackup(bak, "correct-horse", "0.2.0")
	if err != nil {
		t.Fatalf("ExportBackup: %v", err)
	}
	if sum.Servers != 1 {
		t.Fatalf("导出服务器数 = %d，want 1", sum.Servers)
	}

	// 备份文件里不能出现明文凭据
	raw, err := os.ReadFile(bak)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"ssh-secret", "proxy-secret", "47.84.33.255"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("备份文件里出现明文 %q", secret)
		}
	}

	// 模拟"换一台机器"：全新的配置目录导入
	dst := openTestStore(t)
	got, err := dst.ImportBackup(bak, "correct-horse")
	if err != nil {
		t.Fatalf("ImportBackup: %v", err)
	}
	if got.Added != 1 || got.Servers != 1 {
		t.Fatalf("导入统计不对: %+v", got)
	}
	list := dst.List()
	if len(list) != 1 || list[0].ID != id || list[0].Host != "47.84.33.255" {
		t.Fatalf("导入后清单不对: %+v", list)
	}
	pw, key, err := dst.SSHCredentials(id)
	if err != nil || pw != "ssh-secret" || key != "" {
		t.Fatalf("导入后 SSH 密码 = %q key=%q err=%v", pw, key, err)
	}
	user, proxyPw, err := dst.ProxyCredentials(id)
	if err != nil || user != "dukou" || proxyPw != "proxy-secret" {
		t.Fatalf("导入后代理凭据 = %q/%q err=%v", user, proxyPw, err)
	}
}

func TestBackupWrongPassword(t *testing.T) {
	src, _ := seed(t)
	bak := filepath.Join(t.TempDir(), "cfg.dukoubak")
	if _, err := src.ExportBackup(bak, "correct-horse", "0.2.0"); err != nil {
		t.Fatal(err)
	}

	dst := openTestStore(t)
	_, err := dst.ImportBackup(bak, "wrong-password")
	if err == nil || !strings.Contains(err.Error(), "密码不正确") {
		t.Fatalf("错口令应报密码错误，实际: %v", err)
	}
	if len(dst.List()) != 0 {
		t.Fatal("口令错误时不应改动本机清单")
	}
}

func TestBackupRejectsTamperedFile(t *testing.T) {
	src, _ := seed(t)
	bak := filepath.Join(t.TempDir(), "cfg.dukoubak")
	if _, err := src.ExportBackup(bak, "correct-horse", "0.2.0"); err != nil {
		t.Fatal(err)
	}

	var f backupFile
	raw, _ := os.ReadFile(bak)
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	// 翻转密文最后一个字符：GCM 认证必须失败
	data := []byte(f.Data)
	if data[len(data)-2] == 'A' {
		data[len(data)-2] = 'B'
	} else {
		data[len(data)-2] = 'A'
	}
	f.Data = string(data)
	tampered, _ := json.Marshal(f)
	if err := os.WriteFile(bak, tampered, 0o600); err != nil {
		t.Fatal(err)
	}

	dst := openTestStore(t)
	if _, err := dst.ImportBackup(bak, "correct-horse"); err == nil {
		t.Fatal("被篡改的备份包必须拒绝导入")
	}
}

func TestBackupShortPasswordRejected(t *testing.T) {
	src, _ := seed(t)
	_, err := src.ExportBackup(filepath.Join(t.TempDir(), "x.dukoubak"), "123", "0.2.0")
	if err == nil {
		t.Fatal("过短的备份密码必须拒绝")
	}
}

func TestBackupNotABackupFile(t *testing.T) {
	dir := t.TempDir()
	bak := filepath.Join(dir, "junk.dukoubak")
	if err := os.WriteFile(bak, []byte(`{"hello":"world"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openTestStore(t).ImportBackup(bak, "whatever"); err == nil {
		t.Fatal("非备份文件必须拒绝")
	}
}

func TestImportMergesAndKeepsLocalOnly(t *testing.T) {
	src, id := seed(t)
	bak := filepath.Join(t.TempDir(), "cfg.dukoubak")
	if _, err := src.ExportBackup(bak, "correct-horse", "0.2.0"); err != nil {
		t.Fatal(err)
	}

	// 目标机器上已有一台别的服务器
	dst := openTestStore(t)
	local, err := dst.Add(ServerInput{Name: "本机独有", Host: "9.9.9.9", SSHPassword: "x"})
	if err != nil {
		t.Fatal(err)
	}

	sum, err := dst.ImportBackup(bak, "correct-horse")
	if err != nil {
		t.Fatalf("ImportBackup: %v", err)
	}
	if sum.Added != 1 || sum.Updated != 0 || sum.Kept != 1 {
		t.Fatalf("合并统计不对: %+v", sum)
	}
	if len(dst.List()) != 2 {
		t.Fatalf("合并后应有 2 台，实际 %d", len(dst.List()))
	}
	if _, ok := dst.Get(local.ID); !ok {
		t.Fatal("本机独有的服务器被误删")
	}
	if _, ok := dst.Get(id); !ok {
		t.Fatal("导入的服务器缺失")
	}

	// 再导一次：应变成覆盖而不是重复追加
	sum2, err := dst.ImportBackup(bak, "correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	if sum2.Updated != 1 || sum2.Added != 0 {
		t.Fatalf("重复导入应覆盖，实际: %+v", sum2)
	}
	if len(dst.List()) != 2 {
		t.Fatalf("重复导入后数量变了: %d", len(dst.List()))
	}
}

func TestImportWritesSnapshot(t *testing.T) {
	src, _ := seed(t)
	bak := filepath.Join(t.TempDir(), "cfg.dukoubak")
	if _, err := src.ExportBackup(bak, "correct-horse", "0.2.0"); err != nil {
		t.Fatal(err)
	}

	dst, _ := seed(t) // 目标机器已有同名数据，导入会覆盖
	sum, err := dst.ImportBackup(bak, "correct-horse")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Snapshot == "" {
		t.Fatal("导入前必须留快照")
	}
	if _, err := os.Stat(sum.Snapshot + "-servers.json"); err != nil {
		t.Fatalf("快照文件不存在: %v", err)
	}
}
