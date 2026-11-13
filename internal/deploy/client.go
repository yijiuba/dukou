package deploy

import (
	"context"
	"encoding/base64"
	"fmt"
	"runtime"
	"strings"

	"github.com/yijiuba/dukou/internal/gost"
	"github.com/yijiuba/dukou/internal/remote"
	"github.com/yijiuba/dukou/internal/systune"
)

// setupClient 在本机（通常是云电脑）装 gost 客户端、配好代理并自检。
// 返回实测出口 IP，用于证明流量确实走了海外服务器。
func setupClient(ctx context.Context, local *remote.Local, req Request, emit Emitter) (string, error) {
	step := string(StepClient)
	port := 7890

	// ── 权限 ────────────────────────────────────────────────────
	// 装 systemd 单元需要 root，而图形界面一般以普通用户运行。
	// 这里先试探一次，失败就把话说清楚，别等到写文件时才报错。
	emit(Event{Kind: KindLog, Step: step, Msg: "检查本机权限…"})
	if _, err := local.Run("sudo true"); err != nil {
		return "", fmt.Errorf("本机 sudo 权限不可用。请在「本机密码」里填你的登录密码"+
			"（安装服务需要管理员权限）: %w", err)
	}

	// ── 单元名与端口占用 ────────────────────────────────────────
	unit := clientUnit(req, "/usr/local/bin/gost")

	// 端口占用：最常见的是「之前手工配过一套代理」。
	// 危害不是启动失败本身，而是失败之后自检仍然通——
	// 因为 7890 上确实有人在服务，只不过那是旧代理，出口是旧服务器。
	// 于是工具报告「全部完成」，用户拿着一个连错地方的配置去用 Codex。
	//
	// 这一步放在下载之前：端口不通就别白等一次下载。
	if err := ensurePortFree(local, unit.Name, port, port+1); err != nil {
		return "", err
	}

	// ── 安装 gost 客户端 ────────────────────────────────────────
	localArch := runtime.GOARCH
	channel := gost.ChannelFor("unknown") // 本机统一走静态二进制，最稳
	url, err := gostURL(localArch, channel.Ext)
	if err != nil {
		return "", fmt.Errorf("本机架构 %s 不受支持: %w", localArch, err)
	}

	// 先有鸡还是先有蛋：本机此刻还没有任何代理，而 gost 要从 GitHub 下载。
	// 这台机器在国内时直连 GitHub 基本不通，所以第一份文件必须借道
	// 已经装好的服务器——它此刻就是唯一可用的出口。
	emit(Event{Kind: KindLog, Step: step, Msg: "下载 gost 客户端（经服务器中转）…"})
	bootstrap := fmt.Sprintf("-x https://%s:%d --proxy-insecure -U %s:%s",
		req.ServerHost, req.Port, req.User, req.Password)
	viaProxy := fmt.Sprintf(
		`mkdir -p /tmp/dukou && curl -fsSL %s --retry 2 --connect-timeout 10 --max-time 300 -o /tmp/dukou/gost.tar.gz %s`,
		bootstrap, shellQuote(url))
	if _, err := local.Run(viaProxy); err != nil {
		emit(Event{Kind: KindLog, Step: step, Msg: "经服务器下载失败，改试直连…"})
		direct := fmt.Sprintf(
			`mkdir -p /tmp/dukou && curl -fsSL --retry 2 --connect-timeout 8 --max-time 180 -o /tmp/dukou/gost.tar.gz %s`,
			shellQuote(url))
		if _, err2 := local.Run(direct); err2 != nil {
			return "", fmt.Errorf("下载 gost 失败：本机直连 GitHub 不通，"+
				"经服务器 %s:%d 中转也失败。请检查服务器是否正常: %v",
				req.ServerHost, req.Port, err)
		}
	}
	if _, err := local.Run(
		`tar -xzf /tmp/dukou/gost.tar.gz -C /tmp/dukou && ` +
			`sudo install -m 0755 /tmp/dukou/gost /usr/local/bin/gost`); err != nil {
		return "", fmt.Errorf("安装 gost 失败: %w", err)
	}

	// ── systemd 单元 ────────────────────────────────────────────
	if _, err := local.Run(writeFileCommand(unit.Path(), string(unit.Render()), true)); err != nil {
		return "", fmt.Errorf("写入服务配置失败: %w", err)
	}
	// restart 而非 enable --now：服务已在跑时 --now 不会重启，
	// 配置改了却还在用旧值——这是最难自查的一类"改好了"。
	if _, err := local.Run(fmt.Sprintf(
		"sudo systemctl daemon-reload && sudo systemctl enable %s && sudo systemctl restart %s",
		unit.Name, unit.Name)); err != nil {
		return "", fmt.Errorf("启动本机代理服务失败: %w", err)
	}

	// restart 返回 0 只代表 systemd 收下了请求，不代表进程活下来了。
	// 端口冲突时 gost 会立刻退出，systemd 才在几百毫秒后把状态改成 failed。
	if state, err := local.Run("systemctl is-active " + unit.Name); err != nil ||
		strings.TrimSpace(state) != "active" {
		tail, _ := local.Run(fmt.Sprintf(
			"sudo journalctl -u %s -n 12 --no-pager 2>&1 | tail -12", unit.Name))
		return "", fmt.Errorf("服务没有起来（状态 %q）。最近的日志：\n%s",
			strings.TrimSpace(state), strings.TrimSpace(tail))
	}
	// 再确认一次端口确实由我们的单元监听，而不是被别人抢着应答。
	if pid, owner, ok, _ := portOwner(local, port); ok && owner != "" && !sameUnit(owner, unit.Name) {
		return "", fmt.Errorf("端口 %d 仍由 %s（PID %d）监听，本机代理没有真正接管",
			port, owner, pid)
	}
	emit(Event{Kind: KindLog, Step: step, Msg: "本机代理服务已启动（127.0.0.1:" + fmt.Sprint(port) + "）"})

	// ── 内核调优 ────────────────────────────────────────────────
	// 客户端同样要调：上传方向（请求体、模型流式响应的 TCP 窗口）也在本机。
	emit(Event{Kind: KindLog, Step: step, Msg: "调优本机内核 TCP 参数…"})
	if msg, err := systune.Apply(local, true); err != nil {
		emit(Event{Kind: KindLog, Step: step, Msg: "本机内核调优未生效（可用但偏慢）：" + err.Error()})
	} else {
		emit(Event{Kind: KindLog, Step: step, Msg: "拥塞算法 " + msg})
	}

	// ── 系统代理 ────────────────────────────────────────────────
	// 系统代理只影响图形界面程序（浏览器）；Codex 这类命令行工具读的是
	// 环境变量，两处都要配，少一个就会"浏览器能开、Codex 连不上"。
	if _, err := local.Run(gnomeProxyScript(port)); err != nil {
		emit(Event{Kind: KindLog, Step: step, Msg: "跳过系统代理（当前不是 GNOME 桌面）"})
	} else {
		emit(Event{Kind: KindLog, Step: step, Msg: "系统代理已设置（Firefox 需选「使用系统代理设置」）"})
	}

	// ── 命令行环境变量 ──────────────────────────────────────────
	block := proxyEnvBlock()
	enc := base64.StdEncoding.EncodeToString([]byte(block))
	if _, err := local.Run(fmt.Sprintf(
		`grep -q '>>> dukou proxy >>>' "$HOME/.bashrc" || echo '%s' | base64 -d >> "$HOME/.bashrc"`, enc)); err != nil {
		return "", fmt.Errorf("写入 ~/.bashrc 失败: %w", err)
	}
	emit(Event{Kind: KindLog, Step: step, Msg: "已写入命令行代理变量（新开终端生效）"})

	// ── 自检：先确认代理链路，再确认出口 IP ─────────────────────
	emit(Event{Kind: KindLog, Step: step, Msg: "自检代理链路…"})
	code, err := local.Run(fmt.Sprintf(
		`curl -sS -x http://127.0.0.1:%d -o /dev/null -w '%%{http_code}' --max-time 25 https://api.github.com`, port))
	if err != nil {
		return "", fmt.Errorf("代理自检失败: %w", err)
	}
	if got := strings.TrimSpace(code); got != "200" {
		return "", fmt.Errorf("代理已启动但连不通外网（返回 %s）。"+
			"常见原因：服务器端密码与此处不一致，或 8443 端口被中间设备重置。"+
			"排查：sudo journalctl -u %s -n 30", got, unit.Name)
	}

	ip, err := local.Run(fmt.Sprintf(
		`curl -sS -x http://127.0.0.1:%d --max-time 25 https://api.ipify.org`, port))
	if err != nil {
		return "", fmt.Errorf("拿不到出口 IP: %w", err)
	}
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return "", fmt.Errorf("出口 IP 为空，代理可能没生效")
	}

	// 最后一道校验：出口 IP 应当就是用户填的那台服务器。
	// 不一致几乎一定意味着「流量走了别处」（旧代理、NAT、路由劫持）。
	// 不直接判失败，因为云上存在 NAT 网关导致出口与公网 IP 不同的情况，
	// 但必须显式说出来，不能让用户拿着错的配置以为成功了。
	if req.ServerHost != "" && ip != req.ServerHost {
		emit(Event{Kind: KindLog, Step: step, Msg: "⚠ 出口 IP 是 " + ip +
			"，与你填的服务器 " + req.ServerHost + " 不一致，请确认流量是否走了别处"})
	}

	return ip, nil
}

// sameUnit 比较两个 systemd 单元名。
//
// cgroup 里读到的是 "dukou-client.service"，而 Unit.Name 按约定不带后缀。
// 直接字符串比较会永远不相等，把自家服务误判成别人——
// 于是一个完全正常的重跑被拦下："端口 7890 已被 dukou-client.service 占用，
// 这个服务不是 dukou 装的"。这句话自己就自相矛盾。
func sameUnit(a, b string) bool {
	return strings.TrimSuffix(a, ".service") == strings.TrimSuffix(b, ".service")
}

// ensurePortFree 检查端口是否被「别人」占着。
//
// 自己上次装的服务占着不算冲突——restart 会让它把端口交出来。
// 真正的风险是另一个服务（比如手工配的 gost-client）在上面应答：
// 新服务 bind 失败退出，而自检照样通，拿到的是那个旧服务的出口。
func ensurePortFree(e remote.Exec, selfUnit string, ports ...int) error {
	for _, p := range ports {
		pid, owner, occupied, _ := portOwner(e, p)
		// owner 为空表示看不到占用者，不能当成"自己的"放过
		if !occupied || (owner != "" && sameUnit(owner, selfUnit)) {
			continue
		}
		if owner != "" {
			return fmt.Errorf(
				"本机端口 %d 已被 %s（PID %d）占用，这个服务不是 dukou 装的。\n"+
					"请先停掉它再重试：sudo systemctl disable --now %s\n"+
					"不停掉的话，新服务会因端口冲突启动失败，"+
					"而自检会连到旧代理上，给出一个假的「成功」。", p, owner, pid, owner)
		}
		return fmt.Errorf(
			"本机端口 %d 已被占用（PID %d）。查看占用者：sudo ss -lntp 'sport = :%d'\n"+
				"请先停掉它再重试", p, pid, p)
	}
	return nil
}

// portOwner 查监听某端口的进程与它所属的 systemd 单元。
// 读不到 PID 时 occupied 仍为 true 但 pid 为 0——端口确实有人在用，
// 只是当前权限看不到是谁，这种情况也要拦下来。
func portOwner(e remote.Exec, port int) (pid int, unit string, occupied bool, err error) {
	out, err := e.Run(fmt.Sprintf(
		`sudo ss -lntp 'sport = :%d' 2>/dev/null | grep -o 'pid=[0-9]*' | head -1`, port))
	if err != nil {
		return 0, "", false, err
	}
	line := strings.TrimSpace(out)
	if line == "" {
		// 没有 pid= 有两种可能：端口空着，或看不到进程信息。
		// 用一次纯监听查询把两者区分开。
		listen, _ := e.Run(fmt.Sprintf(
			`sudo ss -lnt 'sport = :%d' 2>/dev/null | tail -n +2 | wc -l`, port))
		if strings.TrimSpace(listen) == "0" {
			return 0, "", false, nil
		}
		return 0, "", true, nil
	}
	fmt.Sscanf(line, "pid=%d", &pid)
	cg, _ := e.Run(fmt.Sprintf(
		`sudo cat /proc/%d/cgroup 2>/dev/null | head -1 | sed 's#.*/##'`, pid))
	unit = strings.TrimSpace(cg)
	return pid, unit, true, nil
}

// gnomeProxyScript 生成设置 GNOME 系统代理的命令序列。
//
// 忽略列表里放的是"不该出国"的目标：国内域名后缀、阿里云内网、
// 私有网段。少了阿里云内网最容易踩坑——apt 走 mirror 时会莫名 503。
func gnomeProxyScript(port int) string {
	return fmt.Sprintf(`
command -v gsettings >/dev/null 2>&1 || exit 1
gsettings set org.gnome.system.proxy mode 'manual' || exit 1
gsettings set org.gnome.system.proxy.http host '127.0.0.1'
gsettings set org.gnome.system.proxy.http port %d
gsettings set org.gnome.system.proxy.https host '127.0.0.1'
gsettings set org.gnome.system.proxy.https port %d
gsettings set org.gnome.system.proxy.socks host '127.0.0.1'
gsettings set org.gnome.system.proxy.socks port 7891
gsettings set org.gnome.system.proxy ignore-hosts "['localhost','127.0.0.0/8','::1','*.cn','*.com.cn','*.aliyun.com','*.aliyuncs.com','*.taobao.com','10.0.0.0/8','172.16.0.0/12','192.168.0.0/16']"
`, port, port)
}

// proxyEnvBlock 是写进 ~/.bashrc 的代理环境变量块。
// 用明显的首尾标记包裹，重复执行不会叠加，卸载时也能整段删掉。
func proxyEnvBlock() string {
	return `
# >>> dukou proxy >>>
export HTTP_PROXY="http://127.0.0.1:7890"
export HTTPS_PROXY="$HTTP_PROXY"
export http_proxy="$HTTP_PROXY"
export https_proxy="$HTTP_PROXY"
export NO_PROXY="localhost,127.0.0.1,::1,.cn,.aliyun.com,.aliyuncs.com,.taobao.com,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"
export no_proxy="$NO_PROXY"
# <<< dukou proxy <<<
`
}
