package app

import (
	"github.com/yijiuba/dukou/internal/proxy"
)

// Status 是连接状态（§5.1 契约）。
//
// 直接复用 proxy.Status 而不是再定义一份：这个结构要跨 Wails 边界传到前端，
// 两份结构体迟早会漂移，届时表现为「界面某个格子永远是空」，
// 而编译期不会有任何提示。
type Status = proxy.Status

// ProxyStatus 返回真实连接状态。
//
// 每一次都是现场判断（进程还在不在、系统代理还指不指着我们），
// 而不是读一份可能过期的落盘记录：进程被强杀、系统代理被别的软件改走，
// 这些都只有现场看才知道。
//
// 顺带做状态对账：现场明明没在连接（gost 死了、被强杀后残留），清单里
// 却还挂着「使用中」，首页与清单会互相矛盾（真机踩过：残留横条说没连上，
// 服务器列表显示使用中）。以现场判断为准，把残留的 active 收回 standby。
func (s *Service) ProxyStatus() (Status, error) {
	st, err := s.drv().Current()
	if err != nil {
		return st, err
	}
	if !st.Connected {
		s.demoteStaleActive()
	}
	return st, nil
}

// demoteStaleActive 把所有仍标记为「使用中」的服务器改回待机。
//
// 只在「现场判定没有连接」时调用；正常断开走 Disconnect（自己会收口），
// 连接成功后也不该出现（clearOtherActive 保证至多一台 active）。
func (s *Service) demoteStaleActive() {
	for _, srv := range s.store.List() {
		if srv.Status == "active" {
			_ = s.store.SetStatus(srv.ID, "standby")
		}
	}
}

// proxyOptions 组装本机代理的运行参数。
// DataDir 与 store 的配置目录一致，gost 二进制与状态快照都放在那里。
func (s *Service) proxyOptions() proxy.Options {
	return proxy.Options{DataDir: s.store.Dir()}
}

// emitStatus 把状态推给界面（事件名见 §5.2）。
func (s *Service) emitStatus(st Status) {
	if s.emit != nil {
		s.emit("proxy:status", st)
	}
}
