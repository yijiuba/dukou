package aitool

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Runner 执行一条命令并返回合并后的输出（stdout + stderr）。
//
// 参数是切片而不是拼好的命令行：AI 工具的版本号、路径里都可能出现空格，
// 拼字符串到某天一定会被某个路径击穿。
//
// 抽成类型是为了让安装/卸载的分支与文案能被单测覆盖——真实执行要跑 npm，
// 测试机上既慢又得联网。
type Runner func(ctx context.Context, extraEnv []string, name string, args ...string) (string, error)

// execRunner 是生产实现。
func execRunner(ctx context.Context, extraEnv []string, name string, args ...string) (string, error) {
	realName, prefix := resolveCmd(name)
	c := exec.CommandContext(ctx, realName, append(prefix, args...)...)
	if len(extraEnv) > 0 {
		c.Env = append(os.Environ(), extraEnv...)
	}
	out, err := c.CombinedOutput()
	return string(out), err
}

// resolveCmd 处理 Windows 上的 .cmd/.bat 包装。
//
// npm 在 Windows 上是一个 npm.cmd 批处理文件，直接 CreateProcess 起不来，
// 必须经 cmd.exe /c。这层如果不做，表现是「装完在终端里能用、在这里查不到」，
// 极难定位。
func resolveCmd(name string) (string, []string) {
	if runtime.GOOS != "windows" {
		return name, nil
	}
	if strings.ContainsAny(name, `\/`) || strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name, nil
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return name, nil
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".cmd", ".bat":
		return "cmd.exe", []string{"/c", p}
	}
	return p, nil
}

// Probe 把「怎么问系统」抽出来：真实实现走 PATH 与进程，测试用假的。
type Probe struct {
	OS       string
	Arch     string
	LookPath func(string) (string, error)
	Run      Runner
}

// SystemProbe 返回操作真实系统的探针。
func SystemProbe() Probe {
	return Probe{
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		LookPath: exec.LookPath,
		Run:      execRunner,
	}
}

// probeTimeout 是单个探测命令的上限。
// 探测只是为了画个状态，卡住五秒以上不如直接说"未知"。
const probeTimeout = 5 * time.Second

// Env 是本机环境快照，一次检测复用给所有工具。
type Env struct {
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	NodeVersion string `json:"nodeVersion"`
	NodeOK      bool   `json:"nodeOk"`
	NPMVersion  string `json:"npmVersion"`
	// Proxy 是本次可用代理地址，空表示未连接。
	// 它是「能不能下载」的关键：国内直连 registry.npmjs.org 经常超时。
	Proxy string `json:"proxy"`
}

// State 是一款工具的检测结果。
type State struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Vendor    string `json:"vendor"`
	Desc      string `json:"desc"`
	Installed bool   `json:"installed"`
	Version   string `json:"version"`
	BinPath   string `json:"binPath"`
	CanInstall bool  `json:"canInstall"`
	// Blocked 是不能安装的原因，已经写成用户能照做的一句话。
	Blocked string `json:"blocked,omitempty"`
	Env     Env    `json:"env"`
}

// DetectAll 检测清单里所有工具。env 只算一次，避免每张卡都去起一次 node。
func DetectAll(ctx context.Context, p Probe, proxyAddr string) []State {
	env := detectEnv(ctx, p, proxyAddr)
	out := make([]State, 0, len(Registry))
	for _, t := range Registry {
		out = append(out, detectOne(ctx, p, t, env))
	}
	return out
}

// DetectOne 检测单款工具（安装/卸载后的复验用）。
func DetectOne(ctx context.Context, p Probe, t Tool, proxyAddr string) State {
	return detectOne(ctx, p, t, detectEnv(ctx, p, proxyAddr))
}

func detectOne(ctx context.Context, p Probe, t Tool, env Env) State {
	st := State{ID: t.ID, Name: t.Name, Vendor: t.Vendor, Desc: t.Desc, Env: env}

	binPath, err := p.LookPath(t.Bin)
	if err == nil && binPath != "" {
		st.Installed = true
		st.BinPath = binPath
		st.Version = firstVersion(runQuiet(ctx, p, nil, binPath, "--version"))
	}

	if !st.Installed {
		switch {
		case env.NPMVersion == "":
			st.Blocked = "本机没有 npm。先装 Node.js（18 以上）再回来，装 Node 会自带 npm。"
		case !env.NodeOK:
			st.Blocked = "Node.js 版本过低（" + env.NodeVersion + "），" + t.Name + " 需要 18 以上。"
		default:
			st.CanInstall = true
		}
	}
	return st
}

// detectEnv 采集 OS/架构/Node/npm。
func detectEnv(ctx context.Context, p Probe, proxyAddr string) Env {
	env := Env{OS: p.OS, Arch: p.Arch, Proxy: proxyAddr}

	if _, err := p.LookPath("node"); err == nil {
		env.NodeVersion = strings.TrimSpace(runQuiet(ctx, p, nil, "node", "-v"))
		env.NodeOK = nodeMajor(env.NodeVersion) >= 18
	}
	if _, err := p.LookPath("npm"); err == nil {
		env.NPMVersion = strings.TrimSpace(runQuiet(ctx, p, nil, "npm", "-v"))
	}
	return env
}

// runQuiet 跑一条命令，失败也只返回已有输出：版本号命令经常以非零退出，
// 但输出是对的（例如没配 config 时的 codex --version）。
func runQuiet(ctx context.Context, p Probe, extraEnv []string, name string, args ...string) string {
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, _ := p.Run(cctx, extraEnv, name, args...)
	return out
}

// firstVersion 从输出里取第一个版本号。
//
// 各工具的格式并不统一：`codex-cli 0.44.0`、`1.0.33 (Claude Code)`、
// `v20.11.1`。取第一段 x.y 或 x.y.z 就够了，界面只需要给用户看一眼。
func firstVersion(out string) string {
	for _, f := range strings.Fields(out) {
		f = strings.Trim(f, "()[]vV,;:")
		if isVersion(f) {
			return f
		}
	}
	return ""
}

// isVersion 判断是否形如 1.2 或 1.2.3。
func isVersion(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// nodeMajor 从 "v20.11.1" 取主版本号。
func nodeMajor(v string) int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	head, _, _ := strings.Cut(v, ".")
	n := 0
	for _, r := range head {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
