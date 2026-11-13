package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRenderParseRoundTrip(t *testing.T) {
	orig := Config{
		Role:     RoleServer,
		Port:     8443,
		User:     "dukou",
		Password: "Abc123456xyz",
		Profile:  "codex",
		Created:  "2026-11-13T16:40:00Z",
	}

	got, err := Parse(orig.Render())
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if got != orig {
		t.Errorf("往返后不一致:\n  got  = %+v\n  want = %+v", got, orig)
	}
}

func TestParseClientRequiresServer(t *testing.T) {
	data := []byte("ROLE=client\nPORT=7890\nPASSWORD=abc\n")
	if _, err := Parse(data); err == nil {
		t.Fatal("客户端缺 SERVER 时应报错")
	}

	data = []byte("ROLE=client\nPORT=7890\nPASSWORD=abc\nSERVER=1.2.3.4\n")
	c, err := Parse(data)
	if err != nil {
		t.Fatalf("带 SERVER 时应成功: %v", err)
	}
	if c.Server != "1.2.3.4" {
		t.Errorf("SERVER 解析错误: %q", c.Server)
	}
}

func TestParseRejectsBadValues(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"无效 ROLE", "ROLE=gateway\nPASSWORD=abc\n"},
		{"缺 PASSWORD", "ROLE=server\nPORT=8443\n"},
		{"端口非数字", "ROLE=server\nPORT=abc\nPASSWORD=x\n"},
		{"端口越界", "ROLE=server\nPORT=70000\nPASSWORD=x\n"},
		{"端口为零", "ROLE=server\nPORT=0\nPASSWORD=x\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.in)); err == nil {
				t.Errorf("应报错但通过了: %q", tc.in)
			}
		})
	}
}

func TestParseIgnoresCommentsAndQuotes(t *testing.T) {
	in := []byte("# 这是注释\n\nROLE = \"server\"\nPORT=8443\nPASSWORD='quoted-pass'\n")
	c, err := Parse(in)
	if err != nil {
		t.Fatalf("Parse 失败: %v", err)
	}
	if c.Role != RoleServer {
		t.Errorf("ROLE 解析错误: %q", c.Role)
	}
	if c.Password != "quoted-pass" {
		t.Errorf("引号未去掉: %q", c.Password)
	}
	if c.Port != 8443 {
		t.Errorf("PORT 解析错误: %d", c.Port)
	}
}

func TestWriteAtomicPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不遵循 POSIX 权限位")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dukou.conf")

	c := Config{Role: RoleServer, Port: 8443, User: "u", Password: "p"}
	if err := Save(path, c); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat 失败: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("权限应为 0600，实际 %o", perm)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	if loaded.Password != "p" {
		t.Errorf("读回内容不对: %+v", loaded)
	}
}

func TestWriteAtomicOverwriteLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dukou.conf")

	if err := WriteAtomic(path, []byte("first"), 0o600); err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}
	if err := WriteAtomic(path, []byte("second"), 0o600); err != nil {
		t.Fatalf("覆盖写入失败: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if string(data) != "second" {
		t.Errorf("内容应为 second，实际 %q", data)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir 失败: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".dukou-") {
			t.Errorf("残留临时文件: %s", e.Name())
		}
	}
}

func TestGeneratePassword(t *testing.T) {
	pw, err := GeneratePassword(16)
	if err != nil {
		t.Fatalf("GeneratePassword 失败: %v", err)
	}
	if len(pw) != 16 {
		t.Fatalf("长度应为 16，实际 %d", len(pw))
	}
	for _, r := range pw {
		if !strings.ContainsRune(passwordAlphabet, r) {
			t.Errorf("出现字符集外字符 %q", r)
		}
	}
	// 排除易混淆字符，这是手工核对两端密码时的实际需求
	for _, bad := range "0OlI" {
		if strings.ContainsRune(pw, bad) {
			t.Errorf("密码含易混淆字符 %q: %s", bad, pw)
		}
	}

	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		p, err := GeneratePassword(16)
		if err != nil {
			t.Fatalf("第 %d 次生成失败: %v", i, err)
		}
		if seen[p] {
			t.Fatalf("生成重复密码: %s", p)
		}
		seen[p] = true
	}
}

func TestGeneratePasswordRejectsBadLength(t *testing.T) {
	if _, err := GeneratePassword(0); err == nil {
		t.Error("长度 0 应报错")
	}
	if _, err := GeneratePassword(-5); err == nil {
		t.Error("负数长度应报错")
	}
}

func TestEnvPairsSorted(t *testing.T) {
	c := Config{Role: RoleServer, Port: 8443, User: "u", Password: "p"}
	pairs := c.Env()
	for i := 1; i < len(pairs); i++ {
		if pairs[i-1][0] > pairs[i][0] {
			t.Fatalf("环境变量未按名字排序: %v", pairs)
		}
	}
	if len(pairs) != 3 {
		t.Errorf("应返回 3 项，实际 %d", len(pairs))
	}
}
