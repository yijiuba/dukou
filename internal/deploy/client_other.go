//go:build !linux && !windows

package deploy

import (
	"context"
	"fmt"
	"runtime"

	"github.com/yijiuba/dukou/internal/remote"
)

// setupClient 在 macOS（及其它尚未适配的平台）上如实报错。
//
// 不返回 nil 装作成功：那样界面会显示四步全绿，而本机其实什么都没配，
// 用户拿着一个「已完成」的界面去装上不了网的 Codex。
func setupClient(ctx context.Context, _ *remote.Local, req Request, emit Emitter) (string, error) {
	step := string(StepClient)
	emit(Event{Kind: KindLog, Step: step, Msg: "当前平台尚未支持本机代理自动配置"})
	return "", fmt.Errorf("%s 上的本机代理尚未实现。\n"+
		"      已支持：Windows（自动接管系统代理）、Linux（systemd 常驻服务）。\n"+
		"      在 macOS 上可先手动连接：gost -L http://127.0.0.1:7890 -F tls://%s:%s@%s:%d",
		runtime.GOOS, req.User, req.Password, req.ServerHost, req.Port)
}

// setupCodex 在 macOS 上暂不自动安装。
func setupCodex(ctx context.Context, _ *remote.Local, emit Emitter) (bool, error) {
	step := string(StepCodex)
	emit(Event{Kind: KindLog, Step: step, Msg: "当前平台暂不自动安装 Codex"})
	return false, fmt.Errorf("%s 上的 Codex 需要手动安装：已装 Node 18+ 时执行 "+
		"npm install -g @openai/codex", runtime.GOOS)
}
