package deploy

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/yijiuba/dukou/internal/remote"
)

// setupCodex 安装 OpenAI 的 Codex 命令行工具。
//
// 只做"装上并确认能跑"，不碰登录：Codex 首次运行会弹浏览器做 OAuth，
// 那一步必须用户本人在场点授权，工具替他点不了。
func setupCodex(ctx context.Context, local *remote.Local, emit Emitter) (bool, error) {
	step := string(StepCodex)

	// 已装就不再动它，避免把用户手工调好的版本覆盖掉
	if out, err := local.Run("command -v codex"); err == nil && strings.TrimSpace(out) != "" {
		emit(Event{Kind: KindLog, Step: step, Msg: "检测到已安装：" + strings.TrimSpace(out)})
		if v, err := local.Run("codex --version 2>/dev/null || true"); err == nil {
			emit(Event{Kind: KindLog, Step: step, Msg: "版本 " + strings.TrimSpace(v)})
		}
		return false, nil
	}

	// Node 是 Codex 的前置依赖，版本过低会在安装后运行时才炸
	nodeVer, err := local.Run("node -v 2>/dev/null || echo none")
	if err != nil || strings.TrimSpace(nodeVer) == "none" {
		return false, fmt.Errorf("本机没有 Node.js。Codex 依赖 Node 18 以上版本，" +
			"请先执行：sudo apt install -y nodejs npm")
	}
	major := nodeMajor(strings.TrimSpace(nodeVer))
	if major > 0 && major < 18 {
		return false, fmt.Errorf("Node.js 版本过低（%s），Codex 需要 18 以上。"+
			"请升级后重试", strings.TrimSpace(nodeVer))
	}
	emit(Event{Kind: KindLog, Step: step, Msg: "Node.js " + strings.TrimSpace(nodeVer)})

	emit(Event{Kind: KindLog, Step: step, Msg: "正在安装 @openai/codex（约 1 分钟）…"})
	// 此时本机 gost-client 已经在 127.0.0.1:7890 上跑着了，让 npm 走它。
	// 否则 npm 直连 registry.npmjs.org 在国内会很慢甚至超时。
	if _, err := local.Run(
		"sudo env HTTP_PROXY=http://127.0.0.1:7890 HTTPS_PROXY=http://127.0.0.1:7890 " +
			"npm install -g @openai/codex"); err != nil {
		return false, fmt.Errorf("npm 安装失败（常见原因：网络不通或 npm 源被限速）。"+
			"可试着先执行 sudo npm config set registry https://registry.npmmirror.com 后重试: %w", err)
	}

	out, err := local.Run("codex --version 2>/dev/null || echo unknown")
	if err != nil {
		return false, fmt.Errorf("装完了但 codex 命令不可用，PATH 可能没包含 npm 全局目录")
	}
	emit(Event{Kind: KindLog, Step: step, Msg: "Codex " + strings.TrimSpace(out)})
	return true, nil
}

// nodeMajor 从 "v20.11.1" 里取出主版本号。
func nodeMajor(v string) int {
	v = strings.TrimPrefix(v, "v")
	head, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(head)
	if err != nil {
		return 0
	}
	return n
}
