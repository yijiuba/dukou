package aitool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Launch 在新终端窗口里启动工具。
//
// 为什么是新窗口而不是在当前进程里起子进程：
//   - Codex / Claude Code 是交互式 TUI，需要一个真正的终端（管光标、读按键）；
//   - 而且 duokou 自己可能随时被用户关掉，工具不该跟着一起死。
//
// 为什么要写脚本而不是直接 start codex：
// 代理环境变量必须进新窗口的环境，Windows 上 `start` 不继承我们设的临时 env
// 到新 cmd 的会话里（它继承，但用户之后手动开的终端不会）。写一个自包含的
// 脚本，用户还能自己拖到桌面上复用。
func Launch(ctx context.Context, p Probe, t Tool, proxyAddr, scriptDir string, emit Emitter) (Result, error) {
	emit.emit(StagePrecheck, "检查 "+t.Name+" 是否已安装")
	env := detectEnv(ctx, p, proxyAddr)
	st := detectOne(ctx, p, t, env)
	if !st.Installed {
		return Result{}, fmt.Errorf("%s 还没安装，请先点「安装」。", t.Name)
	}

	emit.emit(StagePrecheck, "准备新终端窗口（含代理环境变量）")
	script, err := writeLauncher(scriptDir, t, proxyAddr, st.BinPath)
	if err != nil {
		return Result{}, fmt.Errorf("生成启动脚本失败：%v", err)
	}
	if err := openTerminal(script); err != nil {
		return Result{}, fmt.Errorf("打开终端失败：%v\n      可以手动执行：%s", err, script)
	}

	note := "已在新终端窗口启动 " + t.Name + "。首次运行会要求在浏览器里授权登录，那一步只能你自己点。"
	if proxyAddr == "" {
		note += " 注意：当前没有连接代理，登录海外服务大概率会失败——先回首页点「连接」再启动。"
	}
	emit.emit(StageVerify, "已启动")
	return Result{
		ID: t.ID, Installed: true, Version: st.Version, BinPath: st.BinPath,
		Note: note,
	}, nil
}

// launcherPath 返回启动脚本路径。按工具分文件，避免两个工具互相覆盖。
func launcherPath(dir string, t Tool) string {
	return filepath.Join(dir, "launch", "dukou-"+t.ID+launcherExt)
}

// ensureParent 建目录。
func ensureParent(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建目录 %s 失败：%v", filepath.Dir(path), err)
	}
	return nil
}
