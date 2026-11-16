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
func (s *Service) ProxyStatus() (Status, error) {
	return proxy.Current(s.proxyOptions())
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
