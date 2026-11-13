// Package deploy 把「在服务器装服务端 + 在本机装客户端 + 装 AI 工具」串成一条流水线。
//
// 设计要点：每一步都是「先检查、再执行、最后自检」。
// 只做执行不做自检的工具，失败时只能告诉你"装完了"，而真正的问题
// 往往在十分钟后才暴露；自检把失败提前到当场。
package deploy

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/yijiuba/dukou/internal/config"
	"github.com/yijiuba/dukou/internal/gost"
	"github.com/yijiuba/dukou/internal/remote"
	"github.com/yijiuba/dukou/internal/systemd"
)

// StepID 是界面上的四个进度行。
type StepID string

const (
	StepConnect StepID = "connect" // 测试连接
	StepServer  StepID = "server"  // 配置服务器
	StepClient  StepID = "client"  // 配置本机
	StepCodex   StepID = "codex"   // 安装 Codex
)

// 事件类型。界面按 Kind 决定画对勾、转圈还是红叉。
const (
	KindStepStart = "step_start"
	KindStepDone  = "step_done"
	KindStepFail  = "step_fail"
	KindLog       = "log"
)

// Event 是流水线推给界面的唯一消息格式。
type Event struct {
	Kind string `json:"kind"`
	Step string `json:"step"`
	Msg  string `json:"msg"`
	// Detail 是失败时的补充建议，界面用小字灰色显示
	Detail string `json:"detail,omitempty"`
}

// Request 是界面收集到的全部输入。
type Request struct {
	ServerHost  string `json:"serverHost"`
	SSHPort     int    `json:"sshPort"`
	SSHUser     string `json:"sshUser"`
	SSHPassword string `json:"sshPassword"`
	SSHKey      string `json:"sshKey"`
	LocalSudo   string `json:"localSudo"`
	InstallCodex bool  `json:"installCodex"`

	// 高级项，留空取默认值
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// Result 是成功后的汇总，界面用它生成"下一步该做什么"的提示。
type Result struct {
	ServerAddr string `json:"serverAddr"`
	Port       int    `json:"port"`
	User       string `json:"user"`
	Password   string `json:"password"`
	OSPretty   string `json:"osPretty"`
	EgressIP   string `json:"egressIp"`
	CodexDone  bool   `json:"codexDone"`
}

// Emitter 接收进度事件。界面通过 Wails 的事件机制转成前端消息。
type Emitter func(Event)

// Run 执行完整流水线。任何一步失败都会立刻返回，
// 并在返回的 error 中带上「哪一步 + 为什么 + 怎么办」。
func Run(ctx context.Context, req Request, emit Emitter) (Result, error) {
	req = withDefaults(req)

	res, err := runServerPhase(ctx, req, emit)
	if err != nil {
		return res, err
	}

	// ── 第 3 步：配置本机 ────────────────────────────────────────
	emit(Event{Kind: KindStepStart, Step: string(StepClient), Msg: "正在配置本机代理…"})
	local := remote.NewLocal(req.LocalSudo)
	defer local.Close()

	ip, err := setupClient(ctx, local, req, emit)
	if err != nil {
		return res, fail(emit, StepClient, "本机配置失败", err)
	}
	res.EgressIP = ip
	emit(Event{Kind: KindStepDone, Step: string(StepClient), Msg: "本机代理已生效，出口 IP " + ip})

	// ── 第 4 步：安装 Codex ──────────────────────────────────────
	if req.InstallCodex {
		emit(Event{Kind: KindStepStart, Step: string(StepCodex), Msg: "正在检查 Codex…"})
		done, err := setupCodex(ctx, local, emit)
		if err != nil {
			return res, fail(emit, StepCodex, "Codex 安装失败", err)
		}
		res.CodexDone = done
		msg := "已安装并可用"
		if !done {
			msg = "已存在，跳过安装"
		}
		emit(Event{Kind: KindStepDone, Step: string(StepCodex), Msg: msg})
	}

	return res, nil
}

// RunClientOnly 只执行「配置本机」这一步，完全不动服务器。
//
// 用在服务器已经部署好的场合：客户端机器不需要、也不应该拿到服务器的 SSH 凭据。
func RunClientOnly(ctx context.Context, req Request, emit Emitter) (Result, error) {
	req = withDefaults(req)
	res := Result{
		ServerAddr: req.ServerHost,
		Port:       req.Port,
		User:       req.User,
		Password:   req.Password,
	}

	emit(Event{Kind: KindStepStart, Step: string(StepClient), Msg: "正在配置本机代理…"})
	local := remote.NewLocal(req.LocalSudo)
	defer local.Close()

	ip, err := setupClient(ctx, local, req, emit)
	if err != nil {
		return res, fail(emit, StepClient, "本机配置失败", err)
	}
	res.EgressIP = ip
	emit(Event{Kind: KindStepDone, Step: string(StepClient), Msg: "本机代理已生效，出口 IP " + ip})

	if req.InstallCodex {
		emit(Event{Kind: KindStepStart, Step: string(StepCodex), Msg: "正在检查 Codex…"})
		done, err := setupCodex(ctx, local, emit)
		if err != nil {
			return res, fail(emit, StepCodex, "Codex 安装失败", err)
		}
		res.CodexDone = done
		msg := "已安装并可用"
		if !done {
			msg = "已存在，跳过安装"
		}
		emit(Event{Kind: KindStepDone, Step: string(StepCodex), Msg: msg})
	}
	return res, nil
}

// RunServerOnly 只执行「连接服务器 + 配置服务器」两步，不碰本机。
//
// 单独暴露出来有两个用途：
//   - 无界面环境（纯 CLI、服务器上跑）只需要装服务端；
//   - CI 的 L2 真机测试要在临时 ECS 上验证服务端安装，而构建机不是目标客户端。
func RunServerOnly(ctx context.Context, req Request, emit Emitter) (Result, error) {
	return runServerPhase(ctx, withDefaults(req), emit)
}

// runServerPhase 是流水线的前两步：连上服务器，把服务端装好并自检。
func runServerPhase(ctx context.Context, req Request, emit Emitter) (Result, error) {
	var res Result

	// ── 第 1 步：测试连接 ────────────────────────────────────────
	emit(Event{Kind: KindStepStart, Step: string(StepConnect), Msg: "正在连接服务器…"})

	ssh, err := remote.DialSSH(remote.SSHConfig{
		Host:     req.ServerHost,
		Port:     req.SSHPort,
		User:     req.SSHUser,
		Password: req.SSHPassword,
		Key:      req.SSHKey,
	})
	if err != nil {
		return res, fail(emit, StepConnect, "连不上服务器", err)
	}
	defer ssh.Close()

	info, err := remote.Probe(ssh)
	if err != nil {
		return res, fail(emit, StepConnect, "登录成功，但读不到系统信息", err)
	}
	res.OSPretty = info.Pretty
	for _, line := range info.Lines() {
		emit(Event{Kind: KindLog, Step: string(StepConnect), Msg: line})
	}

	if !info.IsRoot {
		return res, fail(emit, StepConnect, "SSH 账号不是 root",
			fmt.Errorf("服务器上的安装操作需要 root 权限。请把 SSH 账号换成 root 重新执行"))
	}
	if !info.Systemd {
		return res, fail(emit, StepConnect, "服务器没有 systemd",
			fmt.Errorf("这是容器或特殊系统，本版本只支持 systemd 发行版"))
	}
	res.ServerAddr = req.ServerHost
	res.Port = req.Port
	res.User = req.User
	res.Password = req.Password

	emit(Event{Kind: KindStepDone, Step: string(StepConnect), Msg: info.Pretty})

	// ── 第 2 步：配置服务器 ──────────────────────────────────────
	emit(Event{Kind: KindStepStart, Step: string(StepServer), Msg: "正在配置服务器…"})
	if err := setupServer(ctx, ssh, info, req, emit); err != nil {
		return res, fail(emit, StepServer, "服务器配置失败", err)
	}
	emit(Event{Kind: KindStepDone, Step: string(StepServer), Msg: "服务端已启动并自检通过"})

	return res, nil
}

// withDefaults 补齐可选项。
func withDefaults(r Request) Request {
	if r.SSHPort == 0 {
		r.SSHPort = 22
	}
	if r.SSHUser == "" {
		r.SSHUser = "root"
	}
	if r.Port == 0 {
		r.Port = 8443
	}
	if r.User == "" {
		r.User = "dukou"
	}
	if r.Password == "" {
		// 生成失败也不能让整个流程停在这里，退化成固定长度弱口令没有意义，
		// 所以这里直接 panic 级别的兜底：返回空串，后面校验会拦住。
		if p, err := config.GeneratePassword(16); err == nil {
			r.Password = p
		}
	}
	return r
}

// fail 统一构造失败事件：一句话说清"哪一步坏了"，详细原因放 Detail。
func fail(emit Emitter, step StepID, msg string, err error) error {
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	emit(Event{Kind: KindStepFail, Step: string(step), Msg: msg, Detail: detail})
	return fmt.Errorf("%s：%w", msg, err)
}

// writeFileCommand 生成"把内容写到远端文件"的命令。
//
// 用 base64 而不是 heredoc：单元文件里有 @ : / 等字符，
// 任何引号转义方案都会在某天被某个字符击穿，base64 只含字母数字加号斜杠，
// 在 shell 里永远安全。
func writeFileCommand(path, content string, sudo bool) string {
	enc := base64.StdEncoding.EncodeToString([]byte(content))
	if sudo {
		return fmt.Sprintf("echo '%s' | base64 -d | sudo tee %s >/dev/null", enc, path)
	}
	return fmt.Sprintf("echo '%s' | base64 -d > %s", enc, path)
}

// clientUnit 生成客户端单元。抽出来是为了让服务器端与本机端共用同一套渲染，
// 避免两处逻辑漂移——密码轮换时最容易出现这种不一致。
func clientUnit(req Request, binPath string) systemd.Unit {
	return systemd.ClientUnit(binPath, req.ServerHost, req.Port, req.User, req.Password, 7890, 7891)
}

// serverUnit 生成服务端单元。
func serverUnit(req Request, binPath string) systemd.Unit {
	return systemd.ServerUnit(binPath, req.Port, req.User, req.Password)
}

// shellQuote 把字符串包成单引号形式，用于嵌入命令。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// gostURL 生成下载地址。版本固定用包内常量，保证与 CLI 行为一致。
func gostURL(arch, ext string) (string, error) {
	return gost.DownloadURL(gost.DefaultVersion, arch, ext)
}
