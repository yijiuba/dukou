//go:build windows

package proxy

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

// inetKey 是 Windows「Internet 选项 → 连接 → 局域网设置」背后的注册表位置。
// 浏览器、Electron 应用、.NET HttpClient 都读这里。
const inetKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// WinINet 的两个刷新通知码。
//
// 改完注册表并不会立刻生效：已经跑着的进程各自缓存了一份配置。
// InternetSetOption 是官方提供的「配置变了，重新读」信号；
// 少这一步的表现是「设置写进去了，但浏览器还是连不上，重启浏览器才好」。
const (
	internetOptionRefresh         = 37
	internetOptionSettingsChanged = 39
)

var (
	modWininet             = syscall.NewLazyDLL("wininet.dll")
	procInternetSetOptionW = modWininet.NewProc("InternetSetOptionW")
)

// refreshInet 通知系统代理配置已变更。
func refreshInet() {
	// 两个码都要发：SETTINGS_CHANGED 告诉系统「注册表变了」，
	// REFRESH 让 WinINet 重新加载。只发一个时，部分环境仍会沿用旧值。
	_, _, _ = procInternetSetOptionW.Call(0, internetOptionSettingsChanged, 0, 0)
	_, _, _ = procInternetSetOptionW.Call(0, internetOptionRefresh, 0, 0)
}

// readSystemProxy 读出当前系统代理设置，用于连接前快照。
func readSystemProxy() (state, error) {
	var s state

	k, err := registry.OpenKey(registry.CURRENT_USER, inetKey, registry.QUERY_VALUE)
	if err != nil {
		// 键不存在是正常的（全新用户配置），按「全空」快照处理，
		// 还原时就知道这些项原本都没有，该删而不是该写。
		return s, nil
	}
	defer k.Close()

	if v, _, err := k.GetIntegerValue("ProxyEnable"); err == nil {
		s.ProxyEnable = int(v)
		s.HadEnable = true
	}
	if v, _, err := k.GetStringValue("ProxyServer"); err == nil {
		s.ProxyServer = v
		s.HadServer = true
	}
	if v, _, err := k.GetStringValue("ProxyOverride"); err == nil {
		s.ProxyOverride = v
		s.HadOverride = true
	}
	if v, _, err := k.GetStringValue("AutoConfigURL"); err == nil {
		s.AutoConfigURL = v
		s.HadAutoConf = true
	}
	return s, nil
}

// applySystemProxy 把系统代理指向本机端口（固定代理模式）。
func applySystemProxy(port int) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, inetKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		// 键不存在就建一个
		k, _, err = registry.CreateKey(registry.CURRENT_USER, inetKey, registry.SET_VALUE|registry.QUERY_VALUE)
		if err != nil {
			return fmt.Errorf("打不开 Internet 设置注册表（可能被安全软件拦截）: %w", err)
		}
	}
	defer k.Close()

	// PAC 优先级高于固定代理。不清掉的话，写进去的 ProxyServer 完全不生效，
	// 而界面上看起来一切正常——这是最难自查的一种失败。
	if _, _, err := k.GetStringValue("AutoConfigURL"); err == nil {
		if err := k.DeleteValue("AutoConfigURL"); err != nil {
			return fmt.Errorf("清除旧的 PAC 配置失败: %w", err)
		}
	}

	if err := k.SetDWordValue("ProxyEnable", 1); err != nil {
		return fmt.Errorf("开启系统代理失败: %w", err)
	}
	server := fmt.Sprintf("127.0.0.1:%d", port)
	if err := k.SetStringValue("ProxyServer", server); err != nil {
		return fmt.Errorf("写入代理地址失败: %w", err)
	}
	if err := k.SetStringValue("ProxyOverride", proxyBypass); err != nil {
		return fmt.Errorf("写入直连列表失败: %w", err)
	}

	refreshInet()
	return nil
}

// applySystemProxyPAC 用 PAC 脚本接管系统代理（分流模式）。
//
// 同时写固定代理（ProxyServer=本机端口）作兜底：不同客户端对
// file:// PAC 的支持不一致——Chromium 系浏览器/WinINet 会加载 PAC
// （国内直连、国外走代理），.NET 系客户端实测会忽略 file:// PAC
// 直接回落到 ProxyServer。兜底指向我们自己正在运行的 gost，
// 任何客户端至少保持「全量走代理」可用，而不是直连裸奔或回落到
// 用户已停用的旧代理。
func applySystemProxyPAC(autoconfigURL string, port int) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, inetKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		k, _, err = registry.CreateKey(registry.CURRENT_USER, inetKey, registry.SET_VALUE|registry.QUERY_VALUE)
		if err != nil {
			return fmt.Errorf("打不开 Internet 设置注册表（可能被安全软件拦截）: %w", err)
		}
	}
	defer k.Close()

	if err := k.SetStringValue("AutoConfigURL", autoconfigURL); err != nil {
		return fmt.Errorf("写入 PAC 配置失败: %w", err)
	}
	if err := k.SetDWordValue("ProxyEnable", 1); err != nil {
		return fmt.Errorf("开启系统代理失败: %w", err)
	}
	if err := k.SetStringValue("ProxyServer", fmt.Sprintf("127.0.0.1:%d", port)); err != nil {
		return fmt.Errorf("写入代理地址失败: %w", err)
	}
	if err := k.SetStringValue("ProxyOverride", proxyBypass); err != nil {
		return fmt.Errorf("写入直连列表失败: %w", err)
	}
	refreshInet()
	return nil
}

// restoreSystemProxy 按快照把系统代理恢复到连接前的样子。
//
// 三条硬要求里的第一条（§四）：还原必须可靠。
// 所以每一项都按「原本有 → 写回原值；原本没有 → 删除」处理，
// 而不是一律写 0 或一律删除——后者会在用户机器上留下与原本不同的配置。
func restoreSystemProxy(s *state) error {
	if s == nil {
		return nil
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, inetKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		k, _, err = registry.CreateKey(registry.CURRENT_USER, inetKey, registry.SET_VALUE|registry.QUERY_VALUE)
		if err != nil {
			return fmt.Errorf("打不开 Internet 设置注册表（可能被安全软件拦截）: %w", err)
		}
	}
	defer k.Close()

	if s.HadEnable {
		if err := k.SetDWordValue("ProxyEnable", uint32(s.ProxyEnable)); err != nil {
			return fmt.Errorf("还原代理开关失败: %w", err)
		}
	} else {
		_ = k.DeleteValue("ProxyEnable")
	}

	if s.HadServer {
		if err := k.SetStringValue("ProxyServer", s.ProxyServer); err != nil {
			return fmt.Errorf("还原代理地址失败: %w", err)
		}
	} else {
		_ = k.DeleteValue("ProxyServer")
	}

	if s.HadOverride {
		if err := k.SetStringValue("ProxyOverride", s.ProxyOverride); err != nil {
			return fmt.Errorf("还原直连列表失败: %w", err)
		}
	} else {
		_ = k.DeleteValue("ProxyOverride")
	}

	if s.HadAutoConf && s.AutoConfigURL != "" {
		if err := k.SetStringValue("AutoConfigURL", s.AutoConfigURL); err != nil {
			return fmt.Errorf("还原 PAC 配置失败: %w", err)
		}
	} else {
		_ = k.DeleteValue("AutoConfigURL")
	}

	refreshInet()
	return nil
}

// systemProxyPointsTo 判断当前系统代理是否指向指定端口。
// 用于「残留检测」：快照还在但代理已经不是我们的，就不该报残留。
func systemProxyPointsTo(port int) bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, inetKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()

	enable, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || enable == 0 {
		return false
	}
	server, _, err := k.GetStringValue("ProxyServer")
	if err != nil {
		return false
	}
	return server == fmt.Sprintf("127.0.0.1:%d", port)
}
