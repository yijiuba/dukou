package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/yijiuba/dukou/internal/proxy"
)

// runDisconnect 断开本机代理并还原系统代理。
//
// 这条命令的价值不只是「收尾」：进程异常退出、断电、被强杀之后，
// 系统代理会留在一个指向死端口的状态，浏览器整个上不了网。
// 此时用户能做的就是跑一次 disconnect 把现场恢复干净。
func runDisconnect(args []string) error {
	dir, err := dataDir()
	if err != nil {
		return err
	}
	o := proxy.Options{DataDir: dir}

	st, err := proxy.Current(o)
	if err != nil {
		return fmt.Errorf("读取代理状态失败: %w", err)
	}
	if !st.Connected && !st.Residue {
		fmt.Println("当前未连接，系统代理无需还原。")
		return nil
	}

	if st.Residue {
		fmt.Println("检测到上次异常退出留下的代理设置，正在清理…")
	}

	if err := proxy.Stop(o); err != nil {
		return fmt.Errorf("还原系统代理失败: %w", err)
	}

	fmt.Println("已断开。系统代理已还原为连接前的设置。")
	return nil
}

// dataDir 返回本工具的数据目录，与 internal/app 的约定保持一致。
func dataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("无法定位用户配置目录：%v", err)
	}
	return filepath.Join(base, "dukou"), nil
}
