//go:build windows

package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yijiuba/dukou/internal/gost"
	"golang.org/x/sys/windows"
)

// createNoWindow 让子进程不弹黑窗。
// 少这个标志，用户每次连接都会看到一个控制台窗口闪出来且不消失。
const createNoWindow = 0x08000000

// DETACHED_PROCESS 让转发进程脱离调用方的进程树。
//
// 这不是可选项：命令行版跑完 deploy 就退出，如果 gost 是普通子进程，
// 会和父进程一起被结束，而系统代理已经指向它——用户拿到的是
// 「部署成功」加一个上不了网的浏览器。GUI 版同理：窗口一关就该断开，
// 但「关窗口」和「退出程序」是两回事，进程生命周期不该被前者决定。
const detachedProcess = 0x00000008

var (
	procMu  sync.Mutex
	curProc *exec.Cmd
)

// Start 起本机转发进程并接管系统代理。
//
// 失败时保证两件事：进程被收掉、系统代理没有被改动半截。
func Start(up Upstream, o Options, fetch Fetcher) (Status, error) {
	o = o.Normalize()
	procMu.Lock()
	defer procMu.Unlock()

	// 重入保护：上一次的进程还在就先收掉，否则两个进程抢同一端口
	if curProc != nil {
		stopProcess()
	}

	bin, err := EnsureBinary(o, fetch)
	if err != nil {
		return Status{}, err
	}

	httpPort, socksPort, err := pickPorts(o.HTTPPort, o.SOCKSPort)
	if err != nil {
		return Status{}, err
	}

	orig, err := readSystemProxy()
	if err != nil {
		return Status{}, err
	}
	// 已有快照 = 系统代理此刻正被本工具接管（重复点连接、或切换线路）。
	// 这时必须沿用旧快照里的「真正的原始值」：重新读注册表只会读到
	// 我们自己刚写进去的 127.0.0.1:7890，把它当成原值存下来，
	// 用户的原有配置就被永久覆盖了——断开后他会发现代理没了，且不知为何。
	if prev, perr := readState(o); perr == nil && prev != nil {
		orig.ProxyEnable = prev.ProxyEnable
		orig.ProxyServer = prev.ProxyServer
		orig.ProxyOverride = prev.ProxyOverride
		orig.AutoConfigURL = prev.AutoConfigURL
		orig.HadEnable = prev.HadEnable
		orig.HadServer = prev.HadServer
		orig.HadOverride = prev.HadOverride
		orig.HadAutoConf = prev.HadAutoConf
	}

	logPath := filepath.Join(o.DataDir, "proxy.log")
	var logf *os.File
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
		logf = f
	}

	args := []string{
		"-L", fmt.Sprintf("http://127.0.0.1:%d", httpPort),
		"-L", fmt.Sprintf("socks5://127.0.0.1:%d", socksPort),
		"-F", fmt.Sprintf("tls://%s:%s@%s:%d", up.User, up.Password, up.Host, up.Port),
	}
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow | detachedProcess,
	}
	if logf != nil {
		cmd.Stdout = logf
		cmd.Stderr = logf
	}
	if err := cmd.Start(); err != nil {
		if logf != nil {
			logf.Close()
		}
		return Status{}, fmt.Errorf("启动本机代理失败（常见原因：安全软件拦截）。详见 %s: %w", logPath, err)
	}
	curProc = cmd

	// 进程退出必须回收，否则句柄泄漏；顺带关掉日志文件
	go func() {
		_ = cmd.Wait()
		if logf != nil {
			logf.Close()
		}
	}()

	if err := waitPort(httpPort, 8*time.Second); err != nil {
		stopProcess()
		tail := readLogTail(logPath, 8)
		return Status{}, fmt.Errorf("代理进程已启动，但没有监听 127.0.0.1:%d。日志：\n%s", httpPort, tail)
	}

	st := &state{
		Since:         sinceNow(),
		ServerID:      up.ID,
		ServerName:    up.Name,
		LocalPort:     httpPort,
		PID:           cmd.Process.Pid,
		ProxyEnable:   orig.ProxyEnable,
		ProxyServer:   orig.ProxyServer,
		ProxyOverride: orig.ProxyOverride,
		AutoConfigURL: orig.AutoConfigURL,
		HadEnable:     orig.HadEnable,
		HadServer:     orig.HadServer,
		HadOverride:   orig.HadOverride,
		HadAutoConf:   orig.HadAutoConf,
	}

	// 顺序不能反：先落盘快照，再动系统代理。
	// 反过来的话，如果写快照前崩溃，系统就带着一个指向死端口的代理，
	// 而没有任何记录能说明原来是什么样子。
	if err := writeState(o, st); err != nil {
		stopProcess()
		return Status{}, fmt.Errorf("保存代理状态失败，已放弃修改系统代理: %w", err)
	}
	// 分流模式（§四）：生成 PAC 脚本 → 系统代理指过去。
	// PAC 文件必须先于 AutoConfigURL 存在，否则 WinINet 加载失败，
	// 部分浏览器会静默回落成直连——看起来一切正常，实际上全走直连。
	_, pacURL, err := WritePAC(o, httpPort)
	if err != nil {
		_ = restoreSystemProxy(st)
		stopProcess()
		_ = clearState(o)
		return Status{}, err
	}
	if err := applySystemProxyPAC(pacURL, httpPort); err != nil {
		_ = restoreSystemProxy(st)
		stopProcess()
		_ = clearState(o)
		return Status{}, err
	}

	ip, _, err := probeVia(httpPort)
	if err != nil {
		// 起了进程、改了系统代理，但实际不通：必须整体回滚，
		// 不能留下「界面显示已连接、浏览器打不开」的状态。
		_ = restoreSystemProxy(st)
		_ = clearState(o)
		stopProcess()
		return Status{}, fmt.Errorf("代理已启动但连不通外网：%w\n日志（%s）：\n%s",
			err, logPath, readLogTail(logPath, 8))
	}

	return Status{
		Connected:  true,
		ServerID:   up.ID,
		ServerName: up.Name,
		LocalPort:  httpPort,
		EgressIP:   ip,
		LatencyMS:  TCPRTTMS(up.Host, up.Port), // TCP 建连 RTT，非 HTTPS 总耗时（见 rtt.go）
		Since:      st.Since,
	}, nil
}

// Stop 停掉本机进程并还原系统代理。
//
// 即使内存里没有进程句柄（App 重启过），也按快照里的 PID 收尾——
// 「上次没断干净」正是最需要还原能力的时候。
func Stop(o Options) error {
	o = o.Normalize()
	procMu.Lock()
	defer procMu.Unlock()

	st, err := readState(o)
	if err != nil {
		return err
	}

	if curProc != nil {
		stopProcess()
	} else if st != nil && st.PID > 0 {
		killPID(st.PID)
	}

	if st == nil {
		// 没有快照，说明本工具从未接管过系统代理。
		//
		// 这里绝不能「顺手还原成无代理」：注册表里那个 127.0.0.1:7890
		// 完全可能是用户自己配的（他手动跑着 gost、或者别的工具也用这个端口）。
		// 擅自删掉就是在他机器上搞破坏，而且删完他也不知道为什么上不了网。
		return nil
	}

	if err := restoreSystemProxy(st); err != nil {
		return err
	}
	RemovePAC(o)
	return clearState(o)
}

// Current 读取当前状态。前端启动时先调一次。
func Current(o Options) (Status, error) {
	o = o.Normalize()

	st, err := readState(o)
	if err != nil {
		return Status{}, err
	}
	if st == nil {
		return Status{}, nil
	}

	// 存活判定：PAC 分流模式下注册表里没有 ProxyServer=本机端口，
	// 要额外认 AutoConfigURL 指向我们的 PAC 文件。
	alive := processAlive(st.PID) && (systemProxyPointsTo(st.LocalPort) || SystemPACIsOurs(o))
	if !alive {
		// 快照还在但链路已断：上次多半是崩溃/强杀/断电。
		// 这时候不能假装「未连接」——系统代理可能还指着死端口，
		// 用户会发现浏览器整个上不了网却不知道为什么。
		return Status{
			Connected:  false,
			Residue:    true,
			ServerID:   st.ServerID,
			ServerName: st.ServerName,
			LocalPort:  st.LocalPort,
		}, nil
	}

	res := Status{
		Connected:  true,
		ServerID:   st.ServerID,
		ServerName: st.ServerName,
		LocalPort:  st.LocalPort,
		Since:      st.Since,
	}
	if ip, _, err := probeVia(st.LocalPort); err == nil {
		res.EgressIP = ip
		// LatencyMS 不在这里填：快照没有服务器地址，app 层 ProxyStatus
		// 按 store 里的 host:port 量真实 RTT（rtt.go）。
	}
	return res, nil
}

// EnsureBinary 返回可用的 gost 可执行文件路径，必要时下载。
func EnsureBinary(o Options, fetch Fetcher) (string, error) {
	o = o.Normalize()
	bin := o.BinPath()

	if v := binaryVersion(bin); v != "" {
		return bin, nil
	}

	rawURL, err := gost.DownloadURLFor(gost.DefaultVersion, "windows", runtime.GOARCH, ".zip")
	if err != nil {
		return "", fmt.Errorf("当前架构 %s 没有对应的 gost 发行版: %w", runtime.GOARCH, err)
	}

	dir := filepath.Dir(bin)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	zipPath := filepath.Join(dir, "gost.zip")

	if fetch != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		if err := fetch(ctx, rawURL, zipPath); err != nil {
			return "", fmt.Errorf("经服务器中转下载 gost 失败: %w", err)
		}
	} else {
		if err := httpDownload(rawURL, zipPath); err != nil {
			return "", err
		}
	}

	if _, err := gost.ExtractZip(zipPath, dir, binName); err != nil {
		return "", err
	}
	_ = os.Remove(zipPath)

	if v := binaryVersion(bin); v == "" {
		return "", fmt.Errorf("gost 已解压到 %s，但无法执行（可能被杀毒软件拦截）", bin)
	}
	return bin, nil
}

// httpDownload 是直连下载的兜底路径。
// 本机在海外或已配好其它代理时可用；国内网络下通常走调用方注入的 Fetcher。
func httpDownload(rawURL, dest string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(rawURL)
	if err != nil {
		return fmt.Errorf("下载 %s 失败（本机无法直连 GitHub，需要经服务器中转）: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载 %s 失败：HTTP %d", rawURL, resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(dest)
		return err
	}
	return f.Close()
}

// binaryVersion 执行 `gost -V` 确认二进制真的可用。
//
// 只看文件是否存在是不够的：杀毒软件会把文件拦下来替换成空壳，
// 或者下载被截断。真正跑一次才知道能不能用。
func binaryVersion(bin string) string {
	if _, err := os.Stat(bin); err != nil {
		return ""
	}
	return gost.DetectInstalledVersion(bin)
}

// probeVia 经本地代理取出口 IP，顺带量一次往返耗时。
func probeVia(port int) (string, int, error) {
	pu, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err != nil {
		return "", 0, err
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(pu)},
		Timeout:   20 * time.Second,
	}

	start := time.Now()
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("取出口 IP 返回 HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", 0, err
	}
	ip := strings.TrimSpace(string(b))
	if net.ParseIP(ip) == nil {
		return "", 0, fmt.Errorf("出口 IP 返回了非 IP 内容 %q", ip)
	}
	return ip, int(time.Since(start).Milliseconds()), nil
}

// waitPort 等待本地端口开始监听。
func waitPort(port int, timeout time.Duration) error {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(120 * time.Millisecond)
	}
	return fmt.Errorf("等待 %s 超时", addr)
}

// stopProcess 收掉当前持有的进程。
func stopProcess() {
	if curProc != nil && curProc.Process != nil {
		_ = curProc.Process.Kill()
	}
	curProc = nil
}

// killPID 按 PID 结束进程。
func killPID(pid int) {
	if pid <= 0 {
		return
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

// processAlive 判断 PID 是否仍在运行。
//
// 用 GetExitCodeProcess 而不是 Signal(0)：Go 在 Windows 上
// 对 Signal(0) 的支持与 Unix 语义不同，用它判断存活会得到假阴性。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	const stillActive = 259
	return code == stillActive
}

// readLogTail 取日志尾部若干行，用于把失败原因直接展示在界面上。
func readLogTail(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "（无日志）"
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := strings.Join(lines, "\n")
	if strings.TrimSpace(out) == "" {
		return "（日志为空）"
	}
	return out
}
