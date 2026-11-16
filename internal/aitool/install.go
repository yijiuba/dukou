package aitool

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// 进度阶段。界面按 Stage 决定显示"检查中 / 下载中 / 安装中 / 校验中"。
const (
	StagePrecheck = "precheck"
	StageDownload = "download"
	StageInstall  = "install"
	StageVerify   = "verify"
	StageRemove   = "remove"
)

// Step 是一条进度，emit 到界面即时显示。
type Step struct {
	Stage string `json:"stage"`
	Msg   string `json:"msg"`
}

// Result 是一次安装/卸载的结果。
type Result struct {
	ID        string `json:"id"`
	Installed bool   `json:"installed"`
	Version   string `json:"version"`
	BinPath   string `json:"binPath"`
	// Changed 表示本次是否真的改动了系统。
	// 用户要知道"我点了按钮，机器上到底发生了什么"。
	Changed bool   `json:"changed"`
	Note    string `json:"note,omitempty"`
}

// Emitter 接收进度；可为 nil。
type Emitter func(Step)

func (e Emitter) emit(stage, msg string) {
	if e != nil {
		e(Step{Stage: stage, Msg: msg})
	}
}

// 各阶段的超时。npm 安装在国内直连时经常卡在最后几百 KB，
// 给足时间但必须有上限——无限等下去的表现是"按钮点了没反应"。
const (
	precheckTimeout = 60 * time.Second
	installTimeout  = 8 * time.Minute
	removeTimeout   = 2 * time.Minute
)

// Install 安装一款工具。
//
// 流程：查环境 → 查下载源 → npm 安装 → 复查。任何一步失败都不留半截：
// npm 自己保证全局安装的原子性，我们要保证的是「失败时如实说失败」。
func Install(ctx context.Context, p Probe, t Tool, proxyAddr string, emit Emitter) (Result, error) {
	emit.emit(StagePrecheck, "检查本机环境（Node / npm / 已装版本）")
	env := detectEnv(ctx, p, proxyAddr)

	if st := detectOne(ctx, p, t, env); st.Installed {
		return Result{
			ID: t.ID, Installed: true, Version: st.Version, BinPath: st.BinPath,
			Note: "已安装 " + nonEmpty(st.Version, "（版本未知）") + "，跳过安装。要换版本请先在终端手动卸载。",
		}, nil
	}

	if env.NPMVersion == "" {
		return Result{}, fmt.Errorf("本机没有 npm，装不了 %s。%s", t.Name,
			"先安装 Node.js 18 以上（自带 npm），装好后回到这里重试。")
	}
	if !env.NodeOK {
		return Result{}, fmt.Errorf("Node.js 版本过低（%s），%s 需要 18 以上。升级 Node 后重试。",
			nonEmpty(env.NodeVersion, "未知"), t.Name)
	}

	// 下载源可达性预检。
	// 直接 npm install 也能跑，但失败时要等几分钟超时、报的是一堆 ENOTFOUND；
	// 先查一次，能在 3 秒内告诉用户「连不上 npm 源，请先连接代理」。
	emit.emit(StageDownload, "检查下载源（npm registry）可达性")
	pctx, cancel := context.WithTimeout(ctx, precheckTimeout)
	latest, err := p.Run(pctx, proxyEnv(proxyAddr), "npm", "view", t.NPM, "version")
	cancel()
	if err != nil {
		return Result{}, fmt.Errorf("连不上 npm 源，无法下载 %s：%v\n"+
			"      国内直连 registry.npmjs.org 经常超时：先回首页点「连接」，或\n"+
			"      执行 npm config set registry https://registry.npmmirror.com 后重试。",
			t.Name, brief(err, latest))
	}

	// 安装。
	emit.emit(StageInstall, "正在下载并安装 "+t.NPM+" "+strings.TrimSpace(latest)+"（约 1 分钟）")
	ictx, icancel := context.WithTimeout(ctx, installTimeout)
	out, err := p.Run(ictx, proxyEnv(proxyAddr), "npm", "install", "-g", t.NPM, "--no-fund", "--no-audit")
	icancel()
	if err != nil {
		return Result{}, fmt.Errorf("npm 安装 %s 失败：%v\n"+
			"      常见原因：磁盘空间不足、npm 全局目录没有写权限（Windows 需管理员终端）、\n"+
			"      或代理在下载中途断开。可先执行 npm install -g %s 看完整报错。",
			t.NPM, brief(err, out), t.NPM)
	}

	// 复查：装完必须能跑。npm 退出码 0 但命令不可用，多半是 PATH 里没有全局 bin。
	emit.emit(StageVerify, "校验 "+t.Bin+" 命令是否可用")
	st := detectOne(ctx, p, t, env)
	if !st.Installed {
		return Result{}, fmt.Errorf("npm 报告安装成功，但 %s 命令仍不可用：\n"+
			"      npm 全局目录 %s 可能不在 PATH 里。重开一个终端或把该目录加入 PATH 后重试。",
			t.Bin, strings.TrimSpace(runQuiet(ctx, p, nil, "npm", "prefix", "-g")))
	}

	emit.emit(StageVerify, "已安装 "+t.Name+" "+nonEmpty(st.Version, "（版本未知）"))
	return Result{
		ID: t.ID, Installed: true, Version: st.Version, BinPath: st.BinPath,
		Changed: true,
		Note:    "已装到 " + st.BinPath + "。首次运行会要你在浏览器里授权登录，那一步需要你自己点。",
	}, nil
}

// Uninstall 卸载一款工具。
//
// 只卸载"看起来是我们装的"那一个：命令还在 PATH 里就明说，
// 不把用户手工下载的版本一起删掉，也不假装删干净了。
func Uninstall(ctx context.Context, p Probe, t Tool, emit Emitter) (Result, error) {
	emit.emit(StagePrecheck, "检查 "+t.Name+" 安装位置")
	env := detectEnv(ctx, p, "")
	before := detectOne(ctx, p, t, env)
	if !before.Installed {
		return Result{ID: t.ID, Installed: false, Note: "本机没有检测到 " + t.Bin + "，无需卸载。"}, nil
	}

	emit.emit(StageRemove, "正在卸载 "+t.NPM)
	rctx, cancel := context.WithTimeout(ctx, removeTimeout)
	out, err := p.Run(rctx, nil, "npm", "uninstall", "-g", t.NPM, "--no-fund", "--no-audit")
	cancel()
	if err != nil {
		return Result{}, fmt.Errorf("卸载 %s 失败：%v\n"+
			"      若是权限问题，请在管理员终端执行 npm uninstall -g %s", t.NPM, brief(err, out), t.NPM)
	}

	emit.emit(StageVerify, "确认命令是否已移除")
	after := detectOne(ctx, p, t, env)
	if after.Installed {
		return Result{
			ID: t.ID, Installed: true, Version: after.Version, BinPath: after.BinPath,
			Changed: true,
			Note: "npm 已卸载该包，但 " + after.BinPath + " 仍然存在——" +
				"它可能来自另一个 Node 版本、或你手动下载的副本。本工具不会删它，请自行确认。",
		}, nil
	}

	emit.emit(StageVerify, "已卸载，命令已从 PATH 消失")
	return Result{ID: t.ID, Installed: false, Changed: true, Note: "已卸载 " + t.Name + "。"}, nil
}

// proxyEnv 把本机代理注入子进程环境。
//
// npm 认 HTTP_PROXY/HTTPS_PROXY（大小写都认，这里两套都给），
// NO_PROXY 保留国内域名直连，避免为了装一个包把所有流量绕出国。
func proxyEnv(proxyAddr string) []string {
	if proxyAddr == "" {
		return nil
	}
	return []string{
		"HTTP_PROXY=" + proxyAddr,
		"HTTPS_PROXY=" + proxyAddr,
		"http_proxy=" + proxyAddr,
		"https_proxy=" + proxyAddr,
		"NO_PROXY=localhost,127.0.0.1,*.cn,*.aliyun.com",
		"no_proxy=localhost,127.0.0.1,*.cn,*.aliyun.com",
	}
}

// brief 把命令输出压成一行，塞进错误文案里。
func brief(err error, out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	// npm 的报错通常最后几行才是有用的（前面全是进度条噪音）
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	msg := strings.TrimSpace(strings.Join(lines, " | "))
	if msg == "" {
		return err.Error()
	}
	return msg
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
