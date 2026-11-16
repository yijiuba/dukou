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

	st, err := proxy.Start(up, s.proxyOptions(), nil)
	if err != nil {
		// 连接失败要把「服务器已标记为使用中」这种事回滚掉，
		// 否则界面会显示一台正在用、实际连不上的线路。
		s.markServerStatus(serverID, "error")
		return Status{}, err
	}

	s.markServerStatus(serverID, "active")
	s.emitStatus(st)
	return st, nil
}

// Disconnect 停代理并严格还原系统代理（§5.1）。
func (s *Service) Disconnect() error {
	current, err := proxy.Current(s.proxyOptions())
	if err != nil {
		return err
	}

	if err := proxy.Stop(s.proxyOptions()); err != nil {
		return err
	}

	if current.ServerID != "" {
		s.markServerStatus(current.ServerID, "standby")
	}
	s.emitStatus(Status{})
	return nil
}

// SwitchServer 已连接时切换线路：先彻底断开，再连新线路（§5.1）。
//
// 顺序不能颠倒：直接连新的会读到「已有快照」，把旧线路的原始代理配置
// 当成新连接的快照，最后还原时回到一个谁也没配过的状态。
func (s *Service) SwitchServer(serverID string) error {
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
