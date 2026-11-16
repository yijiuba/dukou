// Package store 负责桌面端的本地持久化：服务器清单 + 机密（SSH 密码/私钥）。
//
// 设计约束（§五/§十）：
//   - servers.json 里绝不明文存密码/私钥——机密单独放 secrets.json，AES-GCM 加密，
//     密钥由本机机器码派生（R5：keychain 优先是后续增强，回退方案先落地）；
//   - 所有写盘走原子写（临时文件 + rename），进程中途被杀不会留下半截文件；
//   - Server 结构永不携带机密字段——传给前端的只是展示用的清单。
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Server 是一台已保存的服务器。机密（SSHPassword/SSHKey）不在这里，
// 存放见 secretStore；结构体字段与 §5.1 接口契约一一对应。
type Server struct {
	ID        string `json:"id"`   // uuid，本地生成
	Name      string `json:"name"` // "东京 · Vultr"
	Flag      string `json:"flag"` // "JP"，界面角标用
	Host      string `json:"host"`
	SSHPort   int    `json:"sshPort"`
	SSHUser   string `json:"sshUser"`
	AuthType  string `json:"authType"`  // password | key
	ProxyPort int    `json:"proxyPort"` // 本机代理监听端口（如 7890）
	// ServerPort 是服务器上代理监听的端口（部署时的 --port，默认 8443）。
	// 与 ProxyPort 是两回事：一个是远端入口，一个是本机入口。
	// 混成一个字段的后果是本机监听 8443、而上游也连 8443，端口语义全乱。
	ServerPort int    `json:"serverPort"`
	ProxyUser  string `json:"proxyUser"`
	Status    string `json:"status"`    // active | standby | error | locked
	LastCheck string `json:"lastCheck"` // RFC3339
	Locked    bool   `json:"locked"`    // 订阅到期后的只读标记
}

// ServerInput 是新增/编辑服务器时界面提交的输入。
// SSHPassword / SSHKey 只写不读：编辑时留空表示"保持原值不变"。
type ServerInput struct {
	Name        string `json:"name"`
	Flag        string `json:"flag"`
	Host        string `json:"host"`
	SSHUser     string `json:"sshUser"`
	AuthType    string `json:"authType"`
	SSHPort     int    `json:"sshPort"`
	ProxyPort   int    `json:"proxyPort"`
	ServerPort  int    `json:"serverPort"`
	SSHPassword string `json:"sshPassword"` // 只写不读
	SSHKey      string `json:"sshKey"`      // 只写不读
	// ProxyPassword 是部署时生成的代理通道密码，连接时用。
	// 与 SSH 密码同为只写字段，留空表示保持原值。
	ProxyPassword string `json:"proxyPassword"`
}

type envelope struct {
	Version int      `json:"version"`
	Servers []Server `json:"servers"`
}

// Store 管理用户目录下的 servers.json 与 secrets.json。
// 所有方法并发安全。
type Store struct {
	mu      sync.RWMutex
	dir     string
	servers []Server
	secrets *secretStore
}

// Open 打开（必要时创建）配置目录并加载清单。
// servers.json 损坏时不静默丢数据：把坏文件改名留档（.corrupt-<时间戳>），从空清单开始。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("无法创建配置目录 %s：%v", dir, err)
	}
	st := &Store{dir: dir}

	raw, err := os.ReadFile(filepath.Join(dir, "servers.json"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		st.servers = []Server{}
	case err != nil:
		return nil, fmt.Errorf("无法读取服务器清单：%v", err)
	default:
		var env envelope
		if jerr := json.Unmarshal(raw, &env); jerr != nil {
			bak := filepath.Join(dir, "servers.json.corrupt-"+time.Now().Format("20060102-150405"))
			if rerr := os.Rename(filepath.Join(dir, "servers.json"), bak); rerr == nil {
				return nil, fmt.Errorf("服务器清单文件已损坏（原文件留档为 %s），已从空清单开始", filepath.Base(bak))
			}
			return nil, fmt.Errorf("服务器清单文件已损坏，请手动处理 %s 后重启", filepath.Join(dir, "servers.json"))
		}
		if env.Servers == nil {
			env.Servers = []Server{}
		}
		st.servers = env.Servers
	}

	sec, err := openSecretStore(dir)
	if err != nil {
		return nil, err
	}
	st.secrets = sec
	return st, nil
}

// Dir 返回配置目录路径（界面"打开配置目录"用）。
func (st *Store) Dir() string { return st.dir }

// List 返回全部服务器的副本。机密不在其中。
func (st *Store) List() []Server {
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := make([]Server, len(st.servers))
	copy(out, st.servers)
	return out
}

// Get 按 ID 取一台服务器（副本）。
func (st *Store) Get(id string) (Server, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	for _, s := range st.servers {
		if s.ID == id {
			return s, true
		}
	}
	return Server{}, false
}

// Add 新增一台服务器并落盘。机密写入加密库，不进 servers.json。
func (st *Store) Add(in ServerInput) (Server, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	s, err := normalize(in)
	if err != nil {
		return Server{}, err
	}
	if s.AuthType == "password" && in.SSHPassword == "" {
		return Server{}, errors.New("认证方式为密码时，SSH 密码不能为空")
	}
	s.ID = uuid.NewString()
	if s.Status == "" {
		s.Status = "standby"
	}
	if err := st.secrets.set(s.ID, in.SSHPassword, in.SSHKey, in.ProxyPassword); err != nil {
		return Server{}, fmt.Errorf("保存 SSH 凭据失败：%v", err)
	}
	st.servers = append(st.servers, s)
	if err := st.saveLocked(); err != nil {
		st.servers = st.servers[:len(st.servers)-1] // 落盘失败就回滚，不留半截状态
		_ = st.secrets.delete(s.ID)
		return Server{}, err
	}
	return s, nil
}

// Update 按 ID 更新。SSHPassword/SSHKey 为空表示保持原凭据不变。
func (st *Store) Update(id string, in ServerInput) (Server, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	idx := -1
	for i := range st.servers {
		if st.servers[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Server{}, fmt.Errorf("找不到服务器 %s，可能已被删除，请刷新列表", id)
	}
	old := st.servers[idx]

	s, err := normalize(in)
	if err != nil {
		return Server{}, err
	}
	s.ID = id
	s.Status = old.Status
	s.LastCheck = old.LastCheck
	s.Locked = old.Locked

	if in.SSHPassword != "" || in.SSHKey != "" || in.ProxyPassword != "" {
		if err := st.secrets.set(id, in.SSHPassword, in.SSHKey, in.ProxyPassword); err != nil {
			return Server{}, fmt.Errorf("更新 SSH 凭据失败：%v", err)
		}
	}

	prev := st.servers[idx]
	st.servers[idx] = s
	if err := st.saveLocked(); err != nil {
		st.servers[idx] = prev
		return Server{}, err
	}
	return s, nil
}

// Remove 按 ID 删除（含机密）。不存在时报错而不是静默成功。
func (st *Store) Remove(id string) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	idx := -1
	for i := range st.servers {
		if st.servers[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("找不到服务器 %s，可能已被删除，请刷新列表", id)
	}
	prev := st.servers[idx]
	st.servers = append(st.servers[:idx], st.servers[idx+1:]...)
	if err := st.saveLocked(); err != nil {
		st.servers = append(st.servers, prev) // 回滚
		return err
	}
	if err := st.secrets.delete(id); err != nil {
		// 清单已删成功，机密残留可以下次清理，不阻塞删除
		return fmt.Errorf("服务器已删除，但凭据文件清理失败：%v", err)
	}
	return nil
}

// SSHCredentials 取某台服务器的解密凭据（部署/重装时用）。
func (st *Store) SSHCredentials(id string) (password, key string, err error) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	pw, key, _, ok, err := st.secrets.get(id)
	if err != nil {
		return "", "", fmt.Errorf("读取 SSH 凭据失败：%v", err)
	}
	if !ok {
		return "", "", fmt.Errorf("服务器 %s 没有保存过凭据", id)
	}
	return pw, key, nil
}

// SetStatus 更新某台服务器的使用状态（active / standby / error / locked）。
// 只动状态与检查时间，不碰凭据。
func (st *Store) SetStatus(id, status string) error {
	st.mu.Lock()
	defer st.mu.Unlock()

	for i := range st.servers {
		if st.servers[i].ID != id {
			continue
		}
		prev := st.servers[i]
		st.servers[i].Status = status
		st.servers[i].LastCheck = time.Now().Format(time.RFC3339)
		if err := st.saveLocked(); err != nil {
			st.servers[i] = prev
			return err
		}
		return nil
	}
	return fmt.Errorf("找不到服务器 %s，可能已被删除，请刷新列表", id)
}

// ProxyCredentials 取代理通道的用户名与密码。
//
// 这条路不碰 SSH：连接一台已经部署好的服务器时，
// 本机完全不需要、也不应该再接触服务器上的 root 密码。
func (st *Store) ProxyCredentials(id string) (user, password string, err error) {
	st.mu.RLock()
	var srv Server
	found := false
	for _, s := range st.servers {
		if s.ID == id {
			srv, found = s, true
			break
		}
	}
	st.mu.RUnlock()

	if !found {
		return "", "", fmt.Errorf("找不到服务器 %s，可能已被删除，请刷新列表", id)
	}

	pw, ok, err := st.secrets.proxyPassword(id)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", fmt.Errorf("服务器 %s 还没有代理密码，请重新执行一次部署", srv.Name)
	}
	return orDefault(srv.ProxyUser, "dukou"), pw, nil
}

// saveLocked 原子写 servers.json。调用方必须已持有写锁。
func (st *Store) saveLocked() error {
	env := envelope{Version: 1, Servers: st.servers}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化服务器清单失败：%v", err)
	}
	return atomicWrite(filepath.Join(st.dir, "servers.json"), data)
}

// normalize 校验输入并填默认值。默认：SSH 22、本机代理 7890（与部署文档口径一致）。
func normalize(in ServerInput) (Server, error) {
	if in.Name == "" {
		return Server{}, errors.New("服务器名称不能为空")
	}
	if in.Host == "" {
		return Server{}, errors.New("服务器地址不能为空，请填 IP 或域名")
	}
	auth := in.AuthType
	if auth == "" {
		auth = "password"
	}
	if auth != "password" && auth != "key" {
		return Server{}, fmt.Errorf("认证类型只支持 password 或 key，收到 %q", auth)
	}
	// 密码是否必填由调用方区分：Add 必填，Update 留空表示保持原值
	sshPort := in.SSHPort
	if sshPort == 0 {
		sshPort = 22
	}
	proxyPort := in.ProxyPort
	if proxyPort == 0 {
		proxyPort = 7890
	}
	serverPort := in.ServerPort
	if serverPort == 0 {
		serverPort = 8443
	}
	return Server{
		Name:       in.Name,
		Flag:       in.Flag,
		Host:       in.Host,
		SSHPort:    sshPort,
		SSHUser:    orDefault(in.SSHUser, "root"),
		AuthType:   auth,
		ProxyPort:  proxyPort,
		ServerPort: serverPort,
		ProxyUser:  "dukou", // 本机代理认证用户名固定；ProxyInput 契约里由部署结果回填
	}, nil
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
