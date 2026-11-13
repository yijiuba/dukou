// Package config 负责 dukou 配置文件的读写。
//
// 设计要点：
//   - 两端（服务端 / 客户端）共用同一套配置格式，凭据只有一个来源。
//     这是本次人工排障最大的教训——两端密码差一个字符，绕了三轮 407。
//   - 写入必须原子：先写同目录临时文件再 rename，避免中途失败留下半截文件。
//   - 文件权限固定 0600，因为里面存了代理密码。
package config

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Role 区分这台机器是出口服务器还是接通道的客户端。
type Role string

const (
	RoleServer Role = "server"
	RoleClient Role = "client"
)

// Config 是两端共用的配置。
type Config struct {
	Role     Role
	Port     int
	User     string
	Password string
	Server   string // 仅客户端：出口服务器地址
	Profile  string // 要安装的 CLI，如 codex
	Created  string // RFC3339，仅用于排查时判断配置新旧
}

// DefaultPath 返回配置文件的默认位置。
// 服务端与客户端都在 Linux 上运行，固定用 /etc/dukou.conf；
// 非 Linux（开发机跑 doctor / 单元测试）退到用户配置目录。
func DefaultPath() string {
	if runtime.GOOS == "linux" {
		return "/etc/dukou.conf"
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "dukou.conf"
	}
	return filepath.Join(dir, "dukou", "dukou.conf")
}

// Render 把配置渲染成文件内容。
// 键的顺序固定，便于人工 diff 和阅读。
func (c Config) Render() []byte {
	var b strings.Builder
	b.WriteString("# dukou 配置文件，由 dukou 自动生成\n")
	b.WriteString("# 修改后需要重启对应的服务：systemctl restart dukou\n")
	b.WriteString("# 本文件含代理密码，权限应为 0600\n")
	b.WriteString("\n")
	fmt.Fprintf(&b, "ROLE=%s\n", c.Role)
	fmt.Fprintf(&b, "PORT=%d\n", c.Port)
	fmt.Fprintf(&b, "USER=%s\n", c.User)
	fmt.Fprintf(&b, "PASSWORD=%s\n", c.Password)
	if c.Server != "" {
		fmt.Fprintf(&b, "SERVER=%s\n", c.Server)
	}
	if c.Profile != "" {
		fmt.Fprintf(&b, "PROFILE=%s\n", c.Profile)
	}
	if c.Created != "" {
		fmt.Fprintf(&b, "CREATED=%s\n", c.Created)
	}
	return []byte(b.String())
}

// Parse 解析配置文件内容。
// 格式为 key=value，忽略空行与 # 注释，值两端的引号会被去掉。
func Parse(data []byte) (Config, error) {
	var c Config
	kv := make(map[string]string, 8)

	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		kv[strings.ToUpper(strings.TrimSpace(key))] = strings.Trim(strings.TrimSpace(val), `"'`)
	}
	if err := sc.Err(); err != nil {
		return c, err
	}

	c.Role = Role(kv["ROLE"])
	if c.Role != RoleServer && c.Role != RoleClient {
		return c, fmt.Errorf("配置中 ROLE=%q 无效，应为 server 或 client", kv["ROLE"])
	}
	if kv["PORT"] != "" {
		port, err := strconv.Atoi(kv["PORT"])
		if err != nil || port <= 0 || port > 65535 {
			return c, fmt.Errorf("配置中 PORT=%q 不是合法端口", kv["PORT"])
		}
		c.Port = port
	}
	c.User = kv["USER"]
	c.Password = kv["PASSWORD"]
	c.Server = kv["SERVER"]
	c.Profile = kv["PROFILE"]
	c.Created = kv["CREATED"]

	if c.Password == "" {
		return c, fmt.Errorf("配置中缺少 PASSWORD")
	}
	if c.Role == RoleClient && c.Server == "" {
		return c, fmt.Errorf("客户端配置中缺少 SERVER（出口服务器地址）")
	}
	return c, nil
}

// Load 从指定路径读取并解析配置。
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	c, err := Parse(data)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Save 原子写入配置并设为 0600。
func Save(path string, c Config) error {
	return WriteAtomic(path, c.Render(), 0o600)
}

// WriteAtomic 先写同目录临时文件再 rename，保证目标文件要么是旧的完整内容、
// 要么是新的完整内容，绝不出现半截。
// 这是「禁止用 sed 打补丁」原则的实现基础。
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录 %s 失败: %w", dir, err)
	}

	f, err := os.CreateTemp(dir, ".dukou-*.tmp")
	if err != nil {
		return fmt.Errorf("在 %s 创建临时文件失败: %w", dir, err)
	}
	tmp := f.Name()

	cleanup := func() {
		f.Close()
		os.Remove(tmp)
	}

	if _, err := f.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := f.Chmod(perm); err != nil {
		cleanup()
		return fmt.Errorf("设置权限失败: %w", err)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("刷盘失败: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("替换 %s 失败: %w", path, err)
	}
	return nil
}

// passwordAlphabet 去掉了容易看错的 0/O、1/l/I，
// 因为用户经常需要在两台机器之间手工核对这个密码。
const passwordAlphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// GeneratePassword 生成 n 位随机密码，字符集为大小写字母加数字。
// 使用 crypto/rand，不是 math/rand——这个密码会暴露在公网上。
func GeneratePassword(n int) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("密码长度必须为正数")
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("读取随机源失败: %w", err)
	}
	// 对 60 取模会引入极轻微的偏斜（256 % 60 = 16），
	// 对本用途（防扫描爆破）无关紧要，换来的是实现简单。
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = passwordAlphabet[int(b)%len(passwordAlphabet)]
	}
	return string(out), nil
}

// Env 返回一组环境变量形式的值，供 doctor 打印或脚本复用。
func (c Config) Env() [][2]string {
	pairs := [][2]string{
		{"DUKOU_ROLE", string(c.Role)},
		{"DUKOU_PORT", strconv.Itoa(c.Port)},
		{"DUKOU_USER", c.User},
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i][0] < pairs[j][0] })
	return pairs
}
