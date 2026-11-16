package app

import "github.com/yijiuba/dukou/internal/proxy"

// proxyDriver 是本机代理的操作面。
//
// 抽成接口的唯一目的是让「切换线路」的顺序与回滚能被单测覆盖：
// 真实的 proxy.Start 会起进程、改写注册表里的系统代理，测试机上跑不了，
// 而「切换失败后界面是不是显示了两台使用中」「目标不存在时是不是先把人断网了」
// 恰恰是这条路径上最容易出错、又最难在真机上复现的地方。
type proxyDriver interface {
	Start(up proxy.Upstream, fetch proxy.Fetcher) (proxy.Status, error)
	Stop() error
	Current() (proxy.Status, error)
}

// systemProxyDriver 是生产实现，直接转调 internal/proxy。
type systemProxyDriver struct{ o proxy.Options }

func (d systemProxyDriver) Start(up proxy.Upstream, fetch proxy.Fetcher) (proxy.Status, error) {
	return proxy.Start(up, d.o, fetch)
}

func (d systemProxyDriver) Stop() error { return proxy.Stop(d.o) }

func (d systemProxyDriver) Current() (proxy.Status, error) { return proxy.Current(d.o) }

// drv 返回代理驱动：测试注入的优先，否则用真实实现。
func (s *Service) drv() proxyDriver {
	if s.pdrv != nil {
		return s.pdrv
	}
	return systemProxyDriver{o: s.proxyOptions()}
}

// clearOtherActive 把除 keepID 之外仍标记为「使用中」的服务器改回待机。
//
// 为什么需要：Disconnect 靠状态快照里的 ServerID 找回旧线路，快照丢失
// （用户手工删了、旧版本遗留、崩溃后文件损坏）时就找不回来，旧服务器会
// 永远停在 active。界面上出现两台「使用中」比显示断开更让人困惑——
// 用户会以为流量正在从两台机器出去。连接成功后统一收口，保证同一时刻
// 至多一台 active。
func (s *Service) clearOtherActive(keepID string) {
	for _, srv := range s.store.List() {
		if srv.ID == keepID || srv.Status != "active" {
			continue
		}
		_ = s.store.SetStatus(srv.ID, "standby")
	}
}
