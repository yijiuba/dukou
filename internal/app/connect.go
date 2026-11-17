package app

import (
	"errors"
	"fmt"

	"github.com/yijiuba/dukou/internal/proxy"
)

// Connect 起本机代理并接管系统代理（§5.1）。
//
// 只读代理密码，不碰 SSH 凭据：连接一台已部署好的服务器时，
// 本机没有任何理由再去碰那台机器的 root 密码。
func (s *Service) Connect(serverID string) (Status, error) {
	if s.ctx == nil {
		return Status{}, errors.New("应用尚未就绪，请重启后再试")
	}
	select {
	case <-s.ctx.Done():
		return Status{}, errors.New("应用正在退出，无法连接")
	default:
	}

	if serverID == "" {
		return Status{}, errors.New("请先选择一台服务器")
	}
	srv, ok := s.store.Get(serverID)
	if !ok {
		return Status{}, fmt.Errorf("找不到服务器 %s，可能已被删除，请刷新列表", serverID)
	}
	if srv.Status == "pending" {
		return Status{}, errors.New("该服务器还没有部署服务端，请先在「代理服务器」页点「部署」")
	}
	// 订阅锁定（契约 §十六）：到期后仅保留 1 台可切换；评估失败放行（fail-open）。
	if err := s.checkSwitchAllowed(serverID); err != nil {
		return Status{}, err
	}

	user, pass, err := s.store.ProxyCredentials(serverID)
	if err != nil {
		return Status{}, err
	}

	up := proxy.Upstream{
		ID:       srv.ID,
		Name:     srv.Name,
		Host:     srv.Host,
		Port:     srv.ServerPort,
		User:     user,
		Password: pass,
	}

	st, err := s.drv().Start(up, nil)
	if err != nil {
		// 连接失败要把「服务器已标记为使用中」这种事回滚掉，
		// 否则界面会显示一台正在用、实际连不上的线路。
		s.markServerStatus(serverID, "error")
		return Status{}, err
	}

	s.markServerStatus(serverID, "active")
	s.clearOtherActive(serverID)
	s.noteConnected(serverID) // 最近成功连接：锁定时保留 1 台规则的优先项（契约 §十六）
	s.fillLatency(&st)        // 真实延迟 = TCP 建连 RTT（覆盖 proxy 层可能带出的旧口径）
	s.emitStatus(st)
	// 已连接才累计"有效在线"（契约 E9 口径），未连接不上报
	s.startHeartbeat()
	return st, nil
}

// Disconnect 停代理并严格还原系统代理（§5.1）。
func (s *Service) Disconnect() error {
	current, err := s.drv().Current()
	if err != nil {
		return err
	}

	if err := s.drv().Stop(); err != nil {
		return err
	}

	if current.ServerID != "" {
		s.markServerStatus(current.ServerID, "standby")
	}
	s.stopHeartbeat()
	s.emitStatus(Status{})
	return nil
}

// CleanupResidue 一键清理残留代理（§六验收：手动杀进程模拟崩溃 →
// 重启 App 提示残留 → 点清理后系统干净）。
//
// proxy.Stop 按快照收尾：杀掉快照记录的 PID、按快照逐项还原注册表、
// 清理状态与 PAC 文件。清理后重新读状态，把最新情况还给前端。
func (s *Service) CleanupResidue() (Status, error) {
	if err := s.drv().Stop(); err != nil {
		return Status{}, err
	}
	st, err := s.drv().Current()
	if err != nil {
		return Status{}, err
	}
	// 清理即断开：清单里残留的「使用中」当场收口，不等下一次状态查询
	s.demoteStaleActive()
	s.emitStatus(st)
	return st, nil
}

// SwitchServer 已连接时切换线路：先彻底断开，再连新线路（§5.1）。
//
// 顺序不能颠倒：直接连新的会读到「已有快照」，把旧线路的原始代理配置
// 当成新连接的快照，最后还原时回到一个谁也没配过的状态。
func (s *Service) SwitchServer(serverID string) error {
	// 目标不存在时不能先断开：那样用户会从「连着」变成「断了而且没连上」，
	// 而这次切换本来就不该动他当前的线路。先校验，再动手。
	srv, ok := s.store.Get(serverID)
	if !ok {
		return fmt.Errorf("找不到服务器 %s，可能已被删除，请刷新列表", serverID)
	}
	if srv.Status == "pending" {
		return fmt.Errorf("「%s」还没有部署服务端，请先部署再切换", srv.Name)
	}
	// 切换的锁定检查必须在断开之前：若放在 Connect 里才发现锁着，
	// 用户已经先被断开了——「从连着变成断了而且没连上」。
	if err := s.checkSwitchAllowed(serverID); err != nil {
		return err
	}
	if err := s.Disconnect(); err != nil {
		return fmt.Errorf("切换前断开旧线路失败: %w", err)
	}
	_, err := s.Connect(serverID)
	return err
}

// markServerStatus 更新服务器的使用状态。
// 失败不阻断主流程：状态标记只是界面装饰，不该让连接因此失败。
func (s *Service) markServerStatus(serverID, status string) {
	srv, ok := s.store.Get(serverID)
	if !ok {
		return
	}
	_ = s.store.SetStatus(srv.ID, status)
}
