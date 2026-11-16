package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return st
}

func TestAddListRoundtrip(t *testing.T) {
	st := openTestStore(t)
	s, err := st.Add(ServerInput{
		Name: "东京 · Vultr", Flag: "JP", Host: "1.2.3.4",
		AuthType: "password", SSHPassword: "pw-_secret",
		SSHUser: "root", ProxyPort: 7890,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if s.ID == "" || s.Status != "standby" || s.SSHPort != 22 {
		t.Fatalf("Add 默认值不对: %+v", s)
	}
	list := st.List()
	if len(list) != 1 || list[0].Name != "东京 · Vultr" {
		t.Fatalf("List: %+v", list)
	}
	// 重开（模拟重启）数据还在
	st2, err := Open(st.Dir())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if len(st2.List()) != 1 {
		t.Fatal("重启后清单丢失")
	}
	pw, key, err := st2.SSHCredentials(s.ID)
	if err != nil || pw != "pw-_secret" || key != "" {
		t.Fatalf("重启后凭据读不回来: pw=%q key=%q err=%v", pw, key, err)
	}
}

func TestSecretsNeverPlaintext(t *testing.T) {
	st := openTestStore(t)
	s, err := st.Add(ServerInput{Name: "x", Host: "h", AuthType: "password", SSHPassword: "TOP-SECRET-PW"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(st.Dir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "TOP-SECRET-PW") {
		t.Fatal("密码明文出现在 servers.json！")
	}
	if strings.Contains(string(raw), s.ID) == false {
		t.Fatal("servers.json 应包含服务器条目")
	}
	rawSec, err := os.ReadFile(filepath.Join(st.Dir(), "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawSec), "TOP-SECRET-PW") {
		t.Fatal("密码明文出现在 secrets.json！")
	}
	// secrets.json 里条目应与服务器 ID 关联（解密时按 ID 取）
	var m map[string]json.RawMessage
	if err := json.Unmarshal(rawSec, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m[s.ID]; !ok {
		t.Fatal("secrets.json 缺少对应 ID 的条目")
	}
}

func TestUpdateKeepsSecretWhenBlank(t *testing.T) {
	st := openTestStore(t)
	s, _ := st.Add(ServerInput{Name: "a", Host: "h", AuthType: "password", SSHPassword: "old-pw"})
	up, err := st.Update(s.ID, ServerInput{Name: "a2", Host: "h2", AuthType: "password"})
	if err != nil {
		t.Fatalf("Update 留空密码应保持原值: %v", err)
	}
	if up.Name != "a2" || up.Status != s.Status {
		t.Fatalf("Update 结果: %+v", up)
	}
	pw, _, err := st.SSHCredentials(s.ID)
	if err != nil || pw != "old-pw" {
		t.Fatalf("凭据应保持原值: pw=%q err=%v", pw, err)
	}
	// 显式换密码
	if _, err := st.Update(s.ID, ServerInput{Name: "a2", Host: "h2", AuthType: "password", SSHPassword: "new-pw"}); err != nil {
		t.Fatal(err)
	}
	if pw, _, _ = st.SSHCredentials(s.ID); pw != "new-pw" {
		t.Fatalf("换密码失败: %q", pw)
	}
}

func TestRemoveCleansSecret(t *testing.T) {
	st := openTestStore(t)
	s, _ := st.Add(ServerInput{Name: "a", Host: "h", AuthType: "password", SSHPassword: "pw"})
	if err := st.Remove(s.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(st.List()) != 0 {
		t.Fatal("删除后清单应空")
	}
	if _, _, err := st.SSHCredentials(s.ID); err == nil {
		t.Fatal("删除后凭据应不存在")
	}
	if err := st.Remove("no-such-id"); err == nil {
		t.Fatal("删除不存在的 ID 应报错")
	}
}

func TestCorruptServersFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "servers.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(dir)
	if err == nil {
		t.Fatal("损坏文件应返回明确错误")
	}
	// 原文件应留档，不被清空
	entries, _ := os.ReadDir(dir)
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "servers.json.corrupt-") {
			found = true
		}
	}
	if !found {
		t.Fatal("损坏文件应留档 .corrupt-<ts>")
	}
}

func TestConcurrentAccess(t *testing.T) {
	st := openTestStore(t)
	var wg sync.WaitGroup
	errs := make(chan error, 30)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := st.Add(ServerInput{Name: "srv", Host: "h", AuthType: "password", SSHPassword: "pw"})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = st.List()
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("并发 Add 失败: %v", e)
	}
	if len(st.List()) != 10 {
		t.Fatalf("并发后应有 10 台，实际 %d", len(st.List()))
	}
}

func TestValidation(t *testing.T) {
	st := openTestStore(t)
	cases := []struct {
		in   ServerInput
		want string
	}{
		{ServerInput{Host: "h", AuthType: "password"}, "名称"},
		{ServerInput{Name: "n", AuthType: "password"}, "地址"},
		{ServerInput{Name: "n", Host: "h", AuthType: "password"}, "密码"},
		{ServerInput{Name: "n", Host: "h", AuthType: "otp", SSHPassword: "p"}, "认证类型"},
	}
	for _, c := range cases {
		if _, err := st.Add(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("输入 %+v 期望错误含 %q，实际 %v", c.in, c.want, err)
		}
	}
}

func TestAtomicWriteNoTmpLeftover(t *testing.T) {
	dir := t.TempDir()
	if err := atomicWrite(filepath.Join(dir, "a.json"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "a.json" {
		t.Fatalf("原子写后不应残留临时文件: %v", entries)
	}
}
