package cmd

import (
	"fmt"
	"net"
)

// step 打印带序号的步骤标题，保持所有子命令的输出节奏一致。
func step(n, total int, title string) {
	fmt.Printf("\n[%d/%d] %s\n", n, total, title)
}

// detail 打印步骤下的细节行。
func detail(format string, args ...any) {
	fmt.Printf("      "+format+"\n", args...)
}

// checkPortFree 尝试绑定端口，能绑上说明当前无人监听。
// 返回 nil 表示端口可用。
func checkPortFree(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("端口 %d 已被占用", port)
	}
	return ln.Close()
}

// notImplemented 用于 v0.1 尚未落地的子命令，明确告知而不是静默成功。
func notImplemented(name string) error {
	return fmt.Errorf("%s 在 v0.1 尚未实现，当前版本可用的命令见 dukou help", name)
}
