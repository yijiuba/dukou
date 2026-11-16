package app

import (
	"encoding/json"
	"fmt"
	"os"
)

// Status 是连接状态（§5.1 契约中的 proxy.Status）。
// M1 阶段 internal/proxy 还不存在（连接是 M2），状态落盘到 status.json，
// 重启后 ProxyStatus 能还原"上次状态"；M2 接上真代理后，写入口不变，
// 只是把"谁写"换成 proxy 包。
type Status struct {
	Connected  bool   `json:"connected"`
	ServerID   string `json:"serverId"`
	ServerName string `json:"serverName"`
	LocalPort  int    `json:"localPort"`
	EgressIP   string `json:"egressIp"`  // 连接后探测，未连接为空
	LatencyMS  int    `json:"latencyMs"` // 圆环下方数据格
	Since      string `json:"since"`     // RFC3339，用于"已连接 1h23m"
	Residue    bool   `json:"residue"`   // true=检测到上次异常退出留下的代理设置
}

// ProxyStatus 供前端启动时拉取连接状态（M1 无连接能力，返回落盘的上次状态）。
func (s *Service) ProxyStatus() (Status, error) {
	return s.loadStatus()
}

// saveStatus 落盘状态（原子写）。M2 起由连接/断开路径调用。
func (s *Service) saveStatus(st Status) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化连接状态失败：%v", err)
	}
	if err := atomicWriteJSON(s.statusPath(), data); err != nil {
		return err
	}
	if s.emit != nil {
		s.emit("proxy:status", st)
	}
	return nil
}

func (s *Service) loadStatus() (Status, error) {
	s.muStatus.RLock()
	defer s.muStatus.RUnlock()
	raw, err := os.ReadFile(s.statusPath())
	if os.IsNotExist(err) {
		return Status{}, nil // 从未连接过，默认断开态
	}
	if err != nil {
		return Status{}, fmt.Errorf("无法读取连接状态：%v", err)
	}
	var st Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return Status{}, fmt.Errorf("连接状态文件损坏，已按断开处理（可手动删除 status.json）：%v", err)
	}
	return st, nil
}

func (s *Service) statusPath() string {
	return s.store.Dir() + string(os.PathSeparator) + "status.json"
}
