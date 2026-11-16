package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/pbkdf2"
)

// .dukoubak 备份包格式（R6）。
//
// 为什么不能直接把 secrets.json 打进包里：那份密文的密钥是本机机器码派生的，
// 换一台机器（或重装系统）就解不开——而"换机迁移"正是备份最主要的用途。
// 所以包里存的是**明文凭据**，由用户口令派生出的密钥整体加密：
//
//	口令 --PBKDF2-SHA256(21 万轮, 随机盐)--> 256 位密钥
//	明文 payload --AES-256-GCM--> 密文（GCM 自带完整性校验，无需另加 HMAC）
//
// 导入时再用本机机器码派生的密钥重新加密写入 secrets.json，
// 于是备份包既是"可迁移的"，落地后又回到"本机绑定"的状态。
const (
	backupFormat  = "dukou-backup"
	backupVersion = 1
	backupKDF     = "pbkdf2-sha256"
	backupIter    = 210000 // OWASP 2023 对 PBKDF2-SHA256 的建议下限
	minPassLen    = 6
)

// backupAAD 参与 GCM 认证，防止把 v1 的包改成 v2 骗过解析。
var backupAAD = []byte(backupFormat + ":v1")

// BackupSummary 是一次导入/导出的结果，直接展示给用户。
type BackupSummary struct {
	Path       string   `json:"path"`
	ExportedAt string   `json:"exportedAt"`
	AppVersion string   `json:"appVersion"`
	Servers    int      `json:"servers"` // 包内服务器数
	Added      int      `json:"added"`   // 新增到本机的台数
	Updated    int      `json:"updated"` // 覆盖本机同 ID 的台数
	Kept       int      `json:"kept"`    // 本机有、包里没有（保持不动）
	Snapshot   string   `json:"snapshot"`
	Warnings   []string `json:"warnings,omitempty"`
}

// 外层信封：可读的元信息 + 加密的 payload。
type backupFile struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	KDF     string `json:"kdf"`
	Iter    int    `json:"iter"`
	Salt    string `json:"salt"`
	Nonce   string `json:"nonce"`
	Data    string `json:"data"`
}

// 内层 payload：整个结构在密文里，不含人可读的服务器信息。
type backupPayload struct {
	ExportedAt string                 `json:"exportedAt"`
	AppVersion string                 `json:"appVersion"`
	Servers    []Server               `json:"servers"`
	Secrets    map[string]plainSecret `json:"secrets"`
	Config     json.RawMessage        `json:"config,omitempty"`
}

// plainSecret 是凭据在加密包里的形态（明文，因为整个 payload 已加密）。
type plainSecret struct {
	Password string `json:"password,omitempty"`
	Key      string `json:"key,omitempty"`
	Proxy    string `json:"proxy,omitempty"`
}

// SuggestedBackupName 返回建议的备份文件名，如 dukou-20261116-1730.dukoubak。
func SuggestedBackupName() string {
	return "dukou-" + time.Now().Format("20060102-1504") + ".dukoubak"
}

// ExportBackup 把服务器清单、凭据、偏好设置打包到 path。
//
// 凭据打包前需先用本机密钥解密——若本机密钥换了（重装系统），
// 这些条目解不开，此时跳过并记入告警，而不是让整个导出失败：
// 用户至少还能把服务器清单带走。
func (st *Store) ExportBackup(path, password, appVersion string) (BackupSummary, error) {
	if strings.TrimSpace(path) == "" {
		return BackupSummary{}, errors.New("请选择备份文件的保存位置")
	}
	if err := checkPassword(password, "备份密码"); err != nil {
		return BackupSummary{}, err
	}

	st.mu.RLock()
	servers := make([]Server, len(st.servers))
	copy(servers, st.servers)
	st.mu.RUnlock()

	secrets, warnings := st.secrets.exportAll()

	cfg, err := os.ReadFile(filepath.Join(st.dir, "config.json"))
	if err != nil && !os.IsNotExist(err) {
		return BackupSummary{}, fmt.Errorf("读取偏好设置失败：%v", err)
	}

	payload := backupPayload{
		ExportedAt: time.Now().Format(time.RFC3339),
		AppVersion: appVersion,
		Servers:    servers,
		Secrets:    secrets,
		Config:     cfg,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return BackupSummary{}, fmt.Errorf("整理备份内容失败：%v", err)
	}

	enc, err := sealBackup(raw, password)
	if err != nil {
		return BackupSummary{}, err
	}
	if err := atomicWrite(path, enc); err != nil {
		return BackupSummary{}, fmt.Errorf("写入备份文件失败：%v", err)
	}

	return BackupSummary{
		Path:       path,
		ExportedAt: payload.ExportedAt,
		AppVersion: appVersion,
		Servers:    len(servers),
		Warnings:   warnings,
	}, nil
}

// ImportBackup 从 path 恢复配置，按服务器 ID 合并（同 ID 覆盖、新 ID 追加、
// 本机独有的保持不动）。
//
// 导入前先把现有 servers.json / secrets.json 复制一份 .pre-import-<时间戳>：
// 恢复是破坏性操作，用户点错一次不该造成不可逆损失。
func (st *Store) ImportBackup(path, password string) (BackupSummary, error) {
	if strings.TrimSpace(path) == "" {
		return BackupSummary{}, errors.New("请选择要导入的备份文件")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return BackupSummary{}, fmt.Errorf("找不到备份文件 %s", filepath.Base(path))
		}
		return BackupSummary{}, fmt.Errorf("读取备份文件失败：%v", err)
	}

	plain, err := openBackup(raw, password)
	if err != nil {
		return BackupSummary{}, err
	}
	var payload backupPayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		return BackupSummary{}, errors.New("备份内容无法解析，文件可能已损坏")
	}
	for i, s := range payload.Servers {
		if strings.TrimSpace(s.Host) == "" || strings.TrimSpace(s.Name) == "" {
			return BackupSummary{}, fmt.Errorf("备份包第 %d 台服务器缺少名称或地址，已中止导入（未改动本机配置）", i+1)
		}
	}

	snapshot, err := st.snapshotForImport()
	if err != nil {
		return BackupSummary{}, err
	}

	sum := BackupSummary{
		Path:       path,
		ExportedAt: payload.ExportedAt,
		AppVersion: payload.AppVersion,
		Servers:    len(payload.Servers),
		Snapshot:   snapshot,
	}
	if err := st.mergeServers(payload.Servers, &sum); err != nil {
		return BackupSummary{}, err
	}

	// 凭据按包内明文重新加密落盘（换成这台机器的密钥）
	if err := st.secrets.replaceAll(payload.Secrets); err != nil {
		return sum, fmt.Errorf("服务器清单已恢复，但凭据写入失败：%v", err)
	}
	if len(payload.Config) > 0 {
		_ = atomicWrite(filepath.Join(st.dir, "config.json"), payload.Config)
	}
	return sum, nil
}

// mergeServers 按 ID 合并服务器清单：同 ID 覆盖、新 ID 追加、本机独有的不动。
func (st *Store) mergeServers(in []Server, sum *BackupSummary) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	inBackup := make(map[string]bool, len(in))
	for _, s := range in {
		inBackup[s.ID] = true
	}
	for _, s := range st.servers {
		if !inBackup[s.ID] {
			sum.Kept++
		}
	}

	index := make(map[string]int, len(st.servers))
	for i, s := range st.servers {
		index[s.ID] = i
	}
	for _, s := range in {
		if i, ok := index[s.ID]; ok {
			st.servers[i] = s
			sum.Updated++
			continue
		}
		st.servers = append(st.servers, s)
		index[s.ID] = len(st.servers) - 1
		sum.Added++
	}
	return st.saveLocked()
}

// snapshotForImport 复制当前清单与凭据，返回快照前缀（用于告知用户如何回滚）。
func (st *Store) snapshotForImport() (string, error) {
	stamp := time.Now().Format("20060102-150405")
	prefix := filepath.Join(st.dir, "pre-import-"+stamp)
	for _, name := range []string{"servers.json", "secrets.json"} {
		src := filepath.Join(st.dir, name)
		data, err := os.ReadFile(src)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("导入前备份现有配置失败：%v", err)
		}
		if err := atomicWrite(prefix+"-"+name, data); err != nil {
			return "", fmt.Errorf("导入前备份现有配置失败：%v", err)
		}
	}
	return prefix, nil
}

// sealBackup 加密 payload 并序列化为可落盘的 JSON。
func sealBackup(payload []byte, password string) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("生成随机盐失败：%v", err)
	}
	key := pbkdf2.Key([]byte(password), salt, backupIter, 32, sha256.New)
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("生成随机数失败：%v", err)
	}
	sealed := gcm.Seal(nil, nonce, payload, backupAAD)

	return json.MarshalIndent(backupFile{
		Format:  backupFormat,
		Version: backupVersion,
		KDF:     backupKDF,
		Iter:    backupIter,
		Salt:    base64.StdEncoding.EncodeToString(salt),
		Nonce:   base64.StdEncoding.EncodeToString(nonce),
		Data:    base64.StdEncoding.EncodeToString(sealed),
	}, "", "  ")
}

// openBackup 解析并解密备份文件。错误文案全部面向用户。
func openBackup(raw []byte, password string) ([]byte, error) {
	var f backupFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, errors.New("这不是 dukou 的备份文件（格式无法识别）")
	}
	if f.Format != backupFormat {
		return nil, errors.New("这不是 dukou 的备份文件")
	}
	if f.Version != backupVersion {
		return nil, fmt.Errorf("备份包版本为 %d，当前版本只支持 %d，请升级客户端后再导入", f.Version, backupVersion)
	}
	if f.KDF != backupKDF || f.Iter < 10000 {
		return nil, errors.New("备份包的加密参数无法识别，文件可能已损坏")
	}
	salt, err := base64.StdEncoding.DecodeString(f.Salt)
	if err != nil {
		return nil, errors.New("备份包的盐值损坏")
	}
	nonce, err := base64.StdEncoding.DecodeString(f.Nonce)
	if err != nil {
		return nil, errors.New("备份包的随机数损坏")
	}
	data, err := base64.StdEncoding.DecodeString(f.Data)
	if err != nil {
		return nil, errors.New("备份内容损坏")
	}
	key := pbkdf2.Key([]byte(password), salt, f.Iter, 32, sha256.New)
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("备份包的随机数长度异常")
	}
	plain, err := gcm.Open(nil, nonce, data, backupAAD)
	if err != nil {
		// GCM 认证失败只有两种可能：口令错，或文件被改过。
		return nil, errors.New("备份密码不正确，或文件内容已损坏")
	}
	return plain, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化加密算法失败：%v", err)
	}
	return cipher.NewGCM(block)
}

func checkPassword(pw, label string) error {
	if len([]rune(pw)) < minPassLen {
		return fmt.Errorf("%s至少 %d 位——这个密码是备份文件唯一的保护，太短等于没加密", label, minPassLen)
	}
	return nil
}

// exportAll 解出全部明文凭据。解不开的（机器码变了）跳过并给出告警。
func (ss *secretStore) exportAll() (map[string]plainSecret, []string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	out := make(map[string]plainSecret, len(ss.entries))
	var warnings []string
	for id, e := range ss.entries {
		var ps plainSecret
		if e.Password != "" {
			v, err := ss.decrypt(e.Password)
			if err != nil {
				warnings = append(warnings, "有 1 台服务器的 SSH 密码无法解密（本机密钥已变化），已跳过")
				continue
			}
			ps.Password = v
		}
		if e.Key != "" {
			v, err := ss.decrypt(e.Key)
			if err != nil {
				warnings = append(warnings, "有 1 台服务器的 SSH 私钥无法解密（本机密钥已变化），已跳过")
				continue
			}
			ps.Key = v
		}
		if e.Proxy != "" {
			v, err := ss.decrypt(e.Proxy)
			if err != nil {
				warnings = append(warnings, "有 1 台服务器的代理密码无法解密（本机密钥已变化），已跳过")
				continue
			}
			ps.Proxy = v
		}
		if ps.Password == "" && ps.Key == "" && ps.Proxy == "" {
			continue // 空条目没有导出的意义
		}
		out[id] = ps
	}
	return out, warnings
}

// replaceAll 用导入的明文覆盖本机凭据（重新用本机密钥加密）。
//
// 只覆盖 Password / Key / Proxy 三项：secretEntry 里还有账号会话（Session），
// 那是"这台机器当前的登录态"，不属于备份内容，也不该被另一台机器的包顶掉。
func (ss *secretStore) replaceAll(in map[string]plainSecret) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	for id, ps := range in {
		entry := ss.entries[id] // 保留 Session 等其它字段
		entry.Password, entry.Key, entry.Proxy = "", "", ""
		var err error
		if ps.Password != "" {
			if entry.Password, err = ss.encrypt(ps.Password); err != nil {
				return err
			}
		}
		if ps.Key != "" {
			if entry.Key, err = ss.encrypt(ps.Key); err != nil {
				return err
			}
		}
		if ps.Proxy != "" {
			if entry.Proxy, err = ss.encrypt(ps.Proxy); err != nil {
				return err
			}
		}
		ss.entries[id] = entry
	}
	return ss.saveLocked()
}
