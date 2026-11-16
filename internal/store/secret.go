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
	"sync"
)

// secretStore 保存 SSH 密码/私钥：AES-256-GCM 加密，密钥由本机机器码派生（R5 回退方案）。
// 文件 servers.json 同目录 secrets.json，条目为 base64(nonce+密文)。
// 注意：机器码变化（重装系统等）后旧凭据无法解密，会明确报错而不是给垃圾数据。
type secretStore struct {
	mu      sync.Mutex
	path    string
	gcm     cipher.AEAD
	entries map[string]secretEntry
}

type secretEntry struct {
	Password string `json:"pw,omitempty"` // base64(nonce+AES-GCM(密码))
	Key      string `json:"key,omitempty"`
}

func openSecretStore(dir string) (*secretStore, error) {
	key, err := machineKey()
	if err != nil {
		return nil, fmt.Errorf("派生本机加密密钥失败：%v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ss := &secretStore{
		path:    dir + string(os.PathSeparator) + "secrets.json",
		gcm:     gcm,
		entries: map[string]secretEntry{},
	}
	raw, err := os.ReadFile(ss.path)
	if errors.Is(err, os.ErrNotExist) {
		return ss, nil
	}
	if err != nil {
		return nil, fmt.Errorf("无法读取凭据文件：%v", err)
	}
	if jerr := json.Unmarshal(raw, &ss.entries); jerr != nil {
		// 凭据文件损坏不能静默清空——那是用户的服务器密码
		return nil, fmt.Errorf("凭据文件已损坏（%s），请删除该文件后重新录入服务器凭据", ss.path)
	}
	return ss, nil
}

// set 写入并落盘。password/key 允许分别更新：传空串表示该项不变？不——
// 这里语义是"整体覆盖"：只传想保存的项，另一项清空。上层 Update 的
// "留空=保持原值"语义在 Store.Update 里通过"不调用 set"实现。
func (ss *secretStore) set(id, password, key string) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	entry := secretEntry{}
	var err error
	if password != "" {
		if entry.Password, err = ss.encrypt(password); err != nil {
			return err
		}
	}
	if key != "" {
		if entry.Key, err = ss.encrypt(key); err != nil {
			return err
		}
	}
	if entry.Password == "" && entry.Key == "" {
		delete(ss.entries, id)
	} else {
		ss.entries[id] = entry
	}
	return ss.saveLocked()
}

func (ss *secretStore) get(id string) (password, key string, ok bool, err error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	entry, exist := ss.entries[id]
	if !exist {
		return "", "", false, nil
	}
	if entry.Password != "" {
		if password, err = ss.decrypt(entry.Password); err != nil {
			return "", "", true, fmt.Errorf("SSH 密码解密失败（可能系统重装导致机器码变化）：%v", err)
		}
	}
	if entry.Key != "" {
		if key, err = ss.decrypt(entry.Key); err != nil {
			return "", "", true, fmt.Errorf("SSH 私钥解密失败（可能系统重装导致机器码变化）：%v", err)
		}
	}
	return password, key, true, nil
}

func (ss *secretStore) delete(id string) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if _, exist := ss.entries[id]; !exist {
		return nil
	}
	delete(ss.entries, id)
	return ss.saveLocked()
}

func (ss *secretStore) encrypt(plain string) (string, error) {
	nonce := make([]byte, ss.gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := ss.gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

func (ss *secretStore) decrypt(b64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", err
	}
	ns := ss.gcm.NonceSize()
	if len(raw) < ns {
		return "", errors.New("密文长度异常")
	}
	plain, err := ss.gcm.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// saveLocked 原子写 secrets.json。调用方必须持有 ss.mu。
func (ss *secretStore) saveLocked() error {
	data, err := json.MarshalIndent(ss.entries, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(ss.path, data)
}

// machineKey 从本机机器码派生 32 字节 AES 密钥。
// Windows：注册表 MachineGuid；Linux/macOS：/etc/machine-id 等。
// 派生加固定盐，避免直接拿机器码当密钥用。
func machineKey() ([]byte, error) {
	id, err := machineID()
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256([]byte("dukou-secret-v1:" + id))
	return h[:], nil
}
