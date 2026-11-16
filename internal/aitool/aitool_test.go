package aitool

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// fakeMachine 是一台可控的假机器：PATH 里有什么、命令返回什么、哪条命令会失败，
// 全部由测试摆布。真实 npm 装一次要联网一分钟，且会把开发机搞脏。
type fakeMachine struct {
	bins    map[string]string // 可执行名 → 路径
	outputs map[string]string // "name arg1 arg2" → 输出
	fail    map[string]error
	onRun   func(key string) // 命令执行后的钩子（模拟安装会写 PATH）

	calls   []string
	envSeen [][]string
}

func newMachine() *fakeMachine {
	m := &fakeMachine{
		bins:    map[string]string{},
		outputs: map[string]string{},
		fail:    map[string]error{},
	}
	m.bins["node"] = `C:\Program Files\nodejs\node.exe`
	m.bins["npm"] = `C:\Program Files\nodejs\npm.cmd`
	m.outputs["node -v"] = "v20.11.1\n"
	m.outputs["npm -v"] = "10.2.4\n"
	return m
}

func (m *fakeMachine) probe() Probe {
	return Probe{
		OS:   "windows",
		Arch: "amd64",
		LookPath: func(name string) (string, error) {
			if p, ok := m.bins[name]; ok {
				return p, nil
			}
			return "", errors.New("exec: not found in %PATH%")
		},
		Run: func(ctx context.Context, env []string, name string, args ...string) (string, error) {
			key := strings.Join(append([]string{name}, args...), " ")
			m.calls = append(m.calls, key)
			m.envSeen = append(m.envSeen, env)
			if m.onRun != nil {
				m.onRun(key)
			}
			out := m.outputs[key]
			return out, m.fail[key]
		},
	}
}

func (m *fakeMachine) calledWith(prefix string) bool {
	for _, c := range m.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// ── 纯函数 ────────────────────────────────────────────────

func TestFirstVersionFormats(t *testing.T) {
	cases := map[string]string{
		"codex-cli 0.44.0":         "0.44.0",
		"1.0.33 (Claude Code)":     "1.0.33",
		"v20.11.1":                 "20.11.1",
		"版本 2.1":                   "2.1",
		"(unknown)":                "",
		"":                         "",
		"error: command not found": "",
	}
	for in, want := range cases {
		if got := firstVersion(in); got != want {
			t.Errorf("firstVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNodeMajor(t *testing.T) {
	cases := map[string]int{"v20.11.1": 20, "v18": 18, "16.20.0": 16, "": 0, "vx.y": 0}
	for in, want := range cases {
		if got := nodeMajor(in); got != want {
			t.Errorf("nodeMajor(%q) = %d, want %d", in, got, want)
		}
	}
}

// ── 检测 ──────────────────────────────────────────────────

func TestDetectAllNotInstalled(t *testing.T) {
	m := newMachine()
	states := DetectAll(context.Background(), m.probe(), "")

	if len(states) != len(Registry) {
		t.Fatalf("应返回全部 %d 个工具，实际 %d", len(Registry), len(states))
	}
	for _, st := range states {
		if st.Installed {
			t.Fatalf("%s 不应显示已安装", st.Name)
		}
		if !st.CanInstall {
			t.Fatalf("%s 环境满足时应该可安装，blocked=%q", st.Name, st.Blocked)
		}
		if st.Env.NodeVersion != "v20.11.1" || !st.Env.NodeOK {
			t.Fatalf("Node 环境检测不对：%+v", st.Env)
		}
	}
}

func TestDetectAllNoNodeBlocksInstall(t *testing.T) {
	m := newMachine()
	delete(m.bins, "node")
	delete(m.bins, "npm")

	states := DetectAll(context.Background(), m.probe(), "")
	for _, st := range states {
		if st.CanInstall {
			t.Fatalf("%s 没有 npm 时不该显示可安装", st.Name)
		}
		if !strings.Contains(st.Blocked, "没有 npm") {
			t.Fatalf("%s 的阻塞原因应说清缺 npm，实际 %q", st.Name, st.Blocked)
		}
	}
}

func TestDetectAllOldNodeBlocksInstall(t *testing.T) {
	m := newMachine()
	m.outputs["node -v"] = "v16.20.0\n"

	states := DetectAll(context.Background(), m.probe(), "")
	for _, st := range states {
		if st.CanInstall {
			t.Fatalf("%s 在 Node 16 下不该显示可安装", st.Name)
		}
		if !strings.Contains(st.Blocked, "版本过低") {
			t.Fatalf("阻塞原因应提到版本过低，实际 %q", st.Blocked)
		}
	}
}

func TestDetectInstalledVersion(t *testing.T) {
	m := newMachine()
	m.bins["codex"] = `C:\Users\u\AppData\Roaming\npm\codex.cmd`
	m.outputs[`C:\Users\u\AppData\Roaming\npm\codex.cmd --version`] = "codex-cli 0.44.0\n"

	states := DetectAll(context.Background(), m.probe(), "")
	codex := stateOf(t, states, "codex")
	if !codex.Installed || codex.Version != "0.44.0" {
		t.Fatalf("已安装工具应带版本号：%+v", codex)
	}
	if codex.CanInstall {
		t.Fatal("已安装时不应再提示可安装")
	}
}

// ── 安装 ──────────────────────────────────────────────────

func TestInstallRunsPrecheckThenNpmThenVerify(t *testing.T) {
	m := newMachine()
	codex := MustLookup("codex")
	m.outputs["npm view @openai/codex version"] = "0.44.0\n"
	m.outputs["npm install -g @openai/codex --no-fund --no-audit"] = "added 1 package\n"
	// 模拟 npm 真的把命令写进了 PATH
	m.onRun = func(key string) {
		if strings.HasPrefix(key, "npm install -g @openai/codex") {
			m.bins["codex"] = `C:\npm\codex.cmd`
			m.outputs[`C:\npm\codex.cmd --version`] = "codex-cli 0.44.0\n"
		}
	}

	var steps []string
	res, err := Install(context.Background(), m.probe(), codex, "http://127.0.0.1:7890",
		func(s Step) { steps = append(steps, s.Stage) })
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.Installed || !res.Changed || res.Version != "0.44.0" {
		t.Fatalf("结果不对：%+v", res)
	}
	if !strings.Contains(res.Note, "浏览器") {
		t.Fatalf("提示里应说明登录要用户自己点，实际 %q", res.Note)
	}

	// 顺序：查源必须在装之前，装完必须复查
	if !m.calledWith("npm view @openai/codex") {
		t.Fatal("应先检查 npm 源可达性")
	}
	if !m.calledWith("npm install -g @openai/codex") {
		t.Fatal("应执行 npm 全局安装")
	}
	viewIdx, installIdx := indexOf(m.calls, "npm view @openai/codex version"), indexOf(m.calls, "npm install -g @openai/codex --no-fund --no-audit")
	if viewIdx < 0 || installIdx < 0 || viewIdx > installIdx {
		t.Fatalf("查源应早于安装，调用序列 %v", m.calls)
	}

	// 进度阶段要覆盖 预检 → 下载 → 安装 → 校验
	want := []string{StagePrecheck, StageDownload, StageInstall, StageVerify, StageVerify}
	if strings.Join(steps, ",") != strings.Join(want, ",") {
		t.Fatalf("进度阶段不对：%v", steps)
	}
}

func TestInstallSendsProxyToNpm(t *testing.T) {
	m := newMachine()
	m.outputs["npm view @openai/codex version"] = "0.44.0\n"
	m.outputs["npm install -g @openai/codex --no-fund --no-audit"] = "ok"
	m.onRun = func(key string) {
		if strings.HasPrefix(key, "npm install") {
			m.bins["codex"] = `C:\npm\codex.cmd`
		}
	}
	if _, err := Install(context.Background(), m.probe(), MustLookup("codex"), "http://127.0.0.1:7890", nil); err != nil {
		t.Fatalf("Install: %v", err)
	}

	for i, env := range m.envSeen {
		// 只有真要联网的 npm 命令需要代理；node -v / npm -v 这类本地探测不该带
		key := m.calls[i]
		if !strings.HasPrefix(key, "npm view") && !strings.HasPrefix(key, "npm install") && !strings.HasPrefix(key, "npm uninstall") {
			continue
		}
		if !contains(env, "HTTPS_PROXY=http://127.0.0.1:7890") {
			t.Fatalf("第 %d 条命令（%s）没带代理环境变量：%v", i, key, env)
		}
		if !contains(env, "NO_PROXY=localhost,127.0.0.1,*.cn,*.aliyun.com") {
			t.Fatalf("第 %d 条命令缺少 NO_PROXY，国内源会被绕出国", i)
		}
	}
}

func TestInstallSkipsWhenAlreadyInstalled(t *testing.T) {
	m := newMachine()
	m.bins["codex"] = `C:\npm\codex.cmd`
	m.outputs[`C:\npm\codex.cmd --version`] = "codex-cli 0.44.0"

	res, err := Install(context.Background(), m.probe(), MustLookup("codex"), "", nil)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Changed {
		t.Fatal("已安装时不该报告改动系统")
	}
	if !strings.Contains(res.Note, "跳过安装") {
		t.Fatalf("应说明跳过原因，实际 %q", res.Note)
	}
	if m.calledWith("npm install") {
		t.Fatal("已安装时不该再跑 npm install")
	}
}

// 源连不上时要在下载前就失败，并且给出可照做的下一步，
// 而不是让 npm 卡三分钟后抛 ENOTFOUND。
func TestInstallFailsFastWhenRegistryUnreachable(t *testing.T) {
	m := newMachine()
	m.outputs["npm view @openai/codex version"] = "npm ERR! code ENOTFOUND\nnpm ERR! network request failed"
	m.fail["npm view @openai/codex version"] = errors.New("exit status 1")

	_, err := Install(context.Background(), m.probe(), MustLookup("codex"), "", nil)
	if err == nil {
		t.Fatal("源不可达时应报错")
	}
	if !strings.Contains(err.Error(), "连不上 npm 源") {
		t.Fatalf("错误文案应说清是源不可达：%v", err)
	}
	if !strings.Contains(err.Error(), "连接") {
		t.Fatalf("错误文案应给出「先连接代理」的指引：%v", err)
	}
	if m.calledWith("npm install") {
		t.Fatal("预检失败后不该继续安装")
	}
}

// npm 退出 0 但命令查不到：这是 PATH 问题，必须点出来，
// 否则用户会以为装好了、然后在终端里发现 codex 不存在。
func TestInstallReportsBinNotInPath(t *testing.T) {
	m := newMachine()
	m.outputs["npm view @openai/codex version"] = "0.44.0\n"
	m.outputs["npm install -g @openai/codex --no-fund --no-audit"] = "added 1 package"
	m.outputs["npm prefix -g"] = `C:\Users\u\AppData\Roaming\npm`

	_, err := Install(context.Background(), m.probe(), MustLookup("codex"), "", nil)
	if err == nil {
		t.Fatal("装完查不到命令时应报错，不能假装成功")
	}
	if !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("错误文案应指向 PATH：%v", err)
	}
	if !strings.Contains(err.Error(), `C:\Users\u\AppData\Roaming\npm`) {
		t.Fatalf("错误文案应带上 npm 全局目录，方便用户自己加 PATH：%v", err)
	}
}

// ── 卸载 ──────────────────────────────────────────────────

func TestUninstallRemovesPackage(t *testing.T) {
	m := newMachine()
	m.bins["claude"] = `C:\npm\claude.cmd`
	m.outputs[`C:\npm\claude.cmd --version`] = "1.0.33 (Claude Code)"
	m.outputs["npm uninstall -g @anthropic-ai/claude-code --no-fund --no-audit"] = "removed 1 package"
	m.onRun = func(key string) {
		if strings.HasPrefix(key, "npm uninstall") {
			delete(m.bins, "claude")
		}
	}

	res, err := Uninstall(context.Background(), m.probe(), MustLookup("claude"), nil)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if res.Installed || !res.Changed {
		t.Fatalf("卸载后应为未安装且确实改动了系统：%+v", res)
	}
}

// 命令还在 PATH 里说明它来自别的安装位置（另一个 Node 版本、手工下载）。
// 本工具不能去删它，也不能谎称"已卸干净"。
func TestUninstallKeepsForeignBinaryAndSaysSo(t *testing.T) {
	m := newMachine()
	m.bins["codex"] = `C:\tools\codex.exe`
	m.outputs[`C:\tools\codex.exe --version`] = "codex-cli 0.44.0"
	m.outputs["npm uninstall -g @openai/codex --no-fund --no-audit"] = "removed 1 package"

	res, err := Uninstall(context.Background(), m.probe(), MustLookup("codex"), nil)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !res.Installed {
		t.Fatal("命令仍可用时应如实报告还在")
	}
	if !strings.Contains(res.Note, "仍然存在") {
		t.Fatalf("应说明另有副本存在，实际 %q", res.Note)
	}
}

func TestUninstallNoopWhenAbsent(t *testing.T) {
	m := newMachine()
	res, err := Uninstall(context.Background(), m.probe(), MustLookup("codex"), nil)
	if err != nil {
		t.Fatalf("未安装时不该报错：%v", err)
	}
	if res.Changed || res.Installed {
		t.Fatalf("未安装时应无改动：%+v", res)
	}
	if m.calledWith("npm uninstall") {
		t.Fatal("未安装时不该调用 npm uninstall")
	}
}

// ── 启动 ──────────────────────────────────────────────────

func TestLaunchRefusesWhenNotInstalled(t *testing.T) {
	m := newMachine()
	_, err := Launch(context.Background(), m.probe(), MustLookup("codex"), "", t.TempDir(), nil)
	if err == nil {
		t.Fatal("未安装时启动应报错")
	}
	if !strings.Contains(err.Error(), "还没安装") {
		t.Fatalf("错误文案应让用户去装：%v", err)
	}
}

// 启动脚本必须把代理写进环境，否则新窗口里登录海外服务直接失败，
// 而用户完全看不出原因。
func TestWriteLauncherCarriesProxy(t *testing.T) {
	m := newMachine()
	m.bins["codex"] = `C:\npm\codex.cmd`
	m.outputs[`C:\npm\codex.cmd --version`] = "codex-cli 0.44.0"

	dir := t.TempDir()
	path, err := writeLauncher(dir, MustLookup("codex"), "http://127.0.0.1:7890", m.bins["codex"])
	if err != nil {
		t.Fatalf("writeLauncher: %v", err)
	}
	body := readFile(t, path)
	for _, want := range []string{"HTTP_PROXY", "http://127.0.0.1:7890", "codex"} {
		if !strings.Contains(body, want) {
			t.Fatalf("启动脚本缺少 %q：\n%s", want, body)
		}
	}
}

// ── 小工具 ────────────────────────────────────────────────

func stateOf(t *testing.T, states []State, id string) State {
	t.Helper()
	for _, st := range states {
		if st.ID == id {
			return st
		}
	}
	t.Fatalf("状态里没有 %s", id)
	return State{}
}

func indexOf(list []string, want string) int {
	for i, v := range list {
		if v == want {
			return i
		}
	}
	return -1
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读 %s: %v", path, err)
	}
	return string(b)
}
