//go:build windows

package deploy

import (
	"context"
	"fmt"

	"github.com/yijiuba/dukou/internal/remote"
)

// setupCodex 在 Windows 上的实现。
//
// 暂不自动安装：Windows 上 Codex 的落地方式取决于用户环境
// （Node/npm、WSL、还是未来官方的原生安装包），
// 贸然去装一个可能会和用户已有的版本打架。
// 这里如实说明，并给出可执行的指引，而不是假装装好了。
func setupCodex(ctx context.Context, _ *remote.Local, emit Emitter) (bool, error) {
	step := string(StepCodex)
	emit(Event{Kind: KindLog, Step: step, Msg: "Windows 版暂不自动安装 Codex"})
	return false, fmt.Errorf("Windows 上的 Codex 需要手动安装：\n" +
		"      已装 Node 18+ 时执行 npm install -g @openai/codex\n" +
		"      或用官方安装包。装好后本机代理已就绪，Codex 可直接使用 " +
		"（需在新开的终端里运行，环境变量才生效）")
}
