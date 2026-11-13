# dukou（渡口）

> **一台海外服务器，让你的电脑连上世界。**
>
> 填几个框，点一下开始。装好代理、配好分流、装好 Codex，
> 最后用一条真实请求证明「确实通了」——而不是丢给你一堆配置文件让你自己猜。

```
你的电脑 / 云电脑                       海外服务器
┌──────────────┐   加密隧道    ┌──────────────────┐
│  浏览器 ────┐ │  ═════════▶  │                  │
│  Codex ─────┤ │              │   dukou 出口     │ ──▶  ChatGPT / Claude
│  终端 ──────┘ │              │                  │ ──▶  GitHub / Google
└──────────────┘              └──────────────────┘
        国内网站 ─────────────────────────────────▶ 直连（不绕路，不变慢）
```

---

## 它能给你什么

一句话：**你只需要一台海外服务器，剩下的它来做。**

| 你得到的 | 具体是什么 |
|---|---|
| 能上外网 | 浏览器、命令行、Codex 全部走同一条通道 |
| 国内不变慢 | 国内站点和阿里云内网直连，不走海外绕一圈 |
| 不卡 | 自动开 TCP 加速（BBR），跨国链路上通常快一个数量级 |
| 装好就能用 | 可选顺带装 Codex，并真实发一条请求验证 |
| 坏了能说清 | 每一步都自检，失败时告诉你断在哪一层、下一步点哪儿 |

---

## 开工前，准备三样东西

### 1. 一台海外的服务器（VPS / 云服务器）

没有的话，阿里云、腾讯云、Vultr、DigitalOcean 都行，**最便宜的 1 核 1G 就够**。

⚠️ **地域一定要选海外**，这是唯一一个选错就全白费的地方：

| 推荐 | 可以 | 千万别选 |
|---|---|---|
| 新加坡、日本、美国西部 | 中国香港 | **中国大陆任何地域** |

选了中国大陆地域，代理会装得很漂亮、全绿，然后一条请求都发不出去。dukou 会在第一步就拦住并告诉你换地域，但换服务器比换地域便宜，别浪费那一步。

### 2. 服务器的登录方式

- **root 密码**（最常见），或
- **SSH 私钥文件**（有些服务商默认只给密钥）

不知道密码在哪：服务商控制台 → 实例详情 → 重置密码。

### 3. 你本机（或云电脑）的登录密码

安装系统服务需要管理员权限，工具会用它执行 `sudo`。
如果你的机器本来就是免密 sudo（部分云电脑镜像如此），这一栏随便填也能过。

---

## 第一步：放行一个端口（唯一必须手动做的一步）

工具会自动配置服务器里的防火墙，但**云平台的安全组在系统外面**，任何工具都改不到——除非你把云账号的 API 密钥交给它，那比手动点两下危险得多。

所以这一步留给你，只需要开一个端口：

```
端口：8443
协议：TCP
来源：0.0.0.0/0
```

**阿里云**：控制台 → 云服务器 ECS → 实例 → 点进实例 → 安全组 → 点安全组 ID → 入方向 → 手动添加 → 按上面填 → 保存

**腾讯云**：控制台 → 轻量应用服务器 → 点实例 → 防火墙 → 添加规则 → TCP:8443

**Vultr / DigitalOcean**：默认没有安全组限制，跳过这步。

> 顺便：SSH 的 22 端口如果对全网开放，装完之后建议改成「我的 IP」，或者换成密钥登录。这不是必须做的，但值得做。

---

## 第二步：下载并运行

### 桌面版（推荐，不用碰命令行）

打开 <https://github.com/yijiuba/dukou/actions>，点最新一次绿色的 `ci`，
页面拉到最底部 **Artifacts**，下载对应你系统的包：

| 你的系统 | 下载 |
|---|---|
| Windows | `dukou-desktop-windows-amd64` |
| Linux（含云电脑 Ubuntu） | `dukou-desktop-linux-amd64` |

下载需要登录 GitHub 账号（GitHub 的规定，不是我们的）。

**Windows**：解压，双击 `dukou-desktop-windows-amd64.exe`。首次运行可能弹 SmartScreen 提示，点「更多信息 → 仍要运行」。

**Linux**：

```bash
unzip dukou-desktop-linux-amd64.zip
chmod +x dukou-desktop-linux-amd64
./dukou-desktop-linux-amd64
```

如果提示缺少图形库（Ubuntu 24.04 上偶尔如此）：

```bash
sudo apt install -y libgtk-3-0 libwebkit2gtk-4.1-0
```

**打开后填五个框**（其余默认即可）：

| 输入框 | 填什么 |
|---|---|
| 服务器 IP | 你服务器的公网 IP，例如 `1.2.3.4` |
| SSH 端口 | 一般就是 `22`，没改过就别动 |
| SSH 账号 | 一般是 `root` |
| SSH 密码 | 服务器的 root 密码 |
| 本机密码 | 你当前电脑的登录密码（用于安装系统服务） |
| ☑ 同时安装 Codex | 需要就勾上 |

展开「高级设置」还能改代理端口、用户名、密码——**不填也没关系**，
密码会随机生成 16 位；想固定密码再填。

只有服务器的 provider 给的是私钥时，勾「使用私钥」并粘贴 `-----BEGIN OPENSSH PRIVATE KEY-----` 开头的那一整段。

点 **开始部署**，然后看四条进度往下走。

> **说明**：桌面版界面已通过构建与单元测试验证，但还没有在足够多的真实环境里跑过。
> 如果它在你机器上启动不了或中途卡住，请直接用下面的命令行版——
> 两者是同一份内核，行为完全一致，命令行版已在真实云电脑上完整跑通。

### 命令行版（已验证，两条命令）

**先拿到 `dukou` 这个命令。** 同样在 Actions 的 Artifacts 里，下载对应系统的包：

| 你的系统 | 下载 | 解压后 |
|---|---|---|
| Linux x86_64（含云电脑） | `dukou-linux-amd64.zip` | `dukou-linux-amd64` |
| Linux ARM64（树莓派、ARM 云主机） | `dukou-linux-arm64.zip` | `dukou-linux-arm64` |
| Linux 32 位 | `dukou-linux-386.zip` | `dukou-linux-386` |

```bash
# 解压后改名放进 PATH，之后就能直接敲 dukou
unzip dukou-linux-amd64.zip
mv dukou-linux-amd64 dukou && chmod +x dukou
sudo mv dukou /usr/local/bin/dukou
```

> **Windows 用户**：没有单独的命令行包。用桌面版即可——
> 它能远程配置服务器，但 Windows 本身没有 systemd，装不了本机代理服务。
> 想在 Windows 上代理，请在 WSL 里按上面的 Linux 步骤操作。

**第一条：在服务器上开出口。** 在**你的本机**执行下面这行，它自己 SSH 上去装：

```bash
dukou deploy --server 1.2.3.4 --ssh-password '你的服务器密码' --server-only
```

它会做这些事：

```
[ ] 正在连接服务器…            ← SSH 登录，读系统版本
[✓] Alibaba Cloud Linux 4.0.7
[ ] 正在配置服务器…
    检查服务器能否出国…          ← 关键：先验证这台机器自己上得了网
    下载并安装 gost…
    调优内核 TCP 参数…
[✓] 服务端已启动并自检通过
```

**第二条：在需要上网的那台机器上接通道。** 换到你的云电脑（或本机）：

```bash
# 从服务器现取密码，不要手抄——抄错一个字符就是这个工具最容易踩的坑
PW=$(ssh root@1.2.3.4 "sed -nE 's#.*dukou:([^@]+)@:8443.*#\1#p' /etc/systemd/system/dukou.service")

dukou deploy --client-only --server 1.2.3.4 --port 8443 \
  --user dukou --password "$PW" --sudo-password '你本机的登录密码'
```

看到下面这行就成功了：

```
[✓] 本机代理已生效，出口 IP 1.2.3.4
```

**这个出口 IP 是实测的**——工具真的发了一条请求出去，问「我是谁」。它必须等于你的服务器 IP。

### 只有私钥，没有密码？

把 `--ssh-password '密码'` 换成：

```bash
--ssh-key ~/.ssh/id_rsa
```

### 想顺带装 Codex

在上面那条客户端命令后面加 `--codex` 即可。

---

## 怎么亲手验证它真的通了

三条命令，看三个结果：

```bash
# 1) 走代理，出口应该是你的服务器 IP
curl -sS -x http://127.0.0.1:7890 https://api.ipify.org; echo

# 2) 走代理，能打开 GitHub
curl -sS -x http://127.0.0.1:7890 -o /dev/null -w "github: %{http_code}\n" https://api.github.com

# 3) 直连（不绕代理），国内站必须还是通的
curl -sS --noproxy '*' -o /dev/null -w "baidu: %{http_code}\n" https://www.baidu.com
```

期望：`1.2.3.4`（你的服务器 IP）、`github: 200`、`baidu: 200`。

第 3 条经常被人忽略，但它是「国内不变慢」唯一的证据。

---

## 让浏览器也走这条通道

代理已经配好了，浏览器只要认它：

| 浏览器 | 怎么设 |
|---|---|
| **Firefox** | 设置 → 网络设置 → 设置 → 选 **使用系统代理设置** |
| **Chrome / Edge** | 跟随系统代理，一般不用动 |
| **终端 / Codex** | 读环境变量，执行 `source ~/.bashrc` 或新开一个终端 |

> 别在浏览器里装一堆代理插件。插件规则和系统代理会互相打架，
> 出现「设置了但不生效」的情况时，排查方向会被彻底带偏。

---

## 装 Codex

部署时勾了「安装 Codex」的话，它已经装好了。没勾的话单独跑：

```bash
dukou deploy --client-only --server 1.2.3.4 --port 8443 \
  --user dukou --password "$PW" --sudo-password '本机密码' --codex
```

然后：

```bash
source ~/.bashrc     # 让代理环境变量生效
codex
```

第一次运行会弹浏览器让你登录 OpenAI。**这一步必须你本人点**——它是账号授权，
任何工具都替代不了，能替代才是安全问题。

登录后如果对话框能正常流式回字，就说明整条链路端到端通了。

---

## 三个最常见的卡点

### ① 部署时提示「公网连不上 8443」

安全组没放行。回到上面「第一步」开端口，然后重跑。

### ② 全绿了，但出口 IP 不是你的服务器

端口上还坐着**以前手工配的代理**。判断方法：

```bash
sudo ss -lntp 'sport = :7890'
```

如果占用者是 `gost-client.service` 之类，停掉它再重跑：

```bash
sudo systemctl disable --now gost-client
```

dukou 会在安装前检查这件事并拦下你，并说明该停哪个服务。

### ③ 能连上，但很慢

跨国链路一丢包，传统 TCP 拥塞算法就会不停缩小窗口，吞吐可能掉一个数量级。dukou 会自动开 BBR，
并在日志里回读确认：看到 `拥塞算法 bbr（已回读确认）` 就是生效了。

如果显示「调优未生效」，按提示看 `journalctl`，或者手动确认：

```bash
sysctl net.ipv4.tcp_congestion_control    # 应为 bbr
```

更多症状和处理见 **[docs/troubleshooting.md](docs/troubleshooting.md)**：
密码两端不一致、明文连接被中间设备重置、改完不重启、把面板锁在门外等，都在里面按症状列好了。

---

## 它不做什么

把边界说清楚，比含糊承诺有用：

- **不替你完成账号登录**。OpenAI、Anthropic 的授权必须你本人点浏览器。
- **不替你开云安全组**。除非你交出 API 密钥，那比手动点两下更危险。
- **不提供账号、不提供服务器**。它只负责把「你已有的服务器」和「你已有的电脑」连起来。
- **不是万能加速器**。它优化的是「国内到海外单向通道」，不会让你的本地宽带变快。

> 请遵守你所在地区的法律法规，以及所用云服务商的服务条款。

---

## 为什么不是「装个代理软件就行」

因为真正让人卡住的从来不是「没有代理」，而是**配了但不通**，而且说不清断在哪。

dukou 的每一步都是「检查 → 执行 → 回读确认」：

| 常见做法 | dukou 的做法 |
|---|---|
| 装完就报成功 | 装完发一条真实请求，回读出口 IP 比对 |
| 只配服务端或只配客户端 | 两边用同一份配置，密码不用手抄 |
| 只装代理不调内核 | 自动开 BBR 并回读确认 |
| 报错是「连接失败」 | 报错是「8443 没放行，去控制台点这里」 |

在丢包明显的跨国链路上，光调一个拥塞算法就能把可用性从「打不开网页」
变成「正常使用」——这不是玄学，是 TCP 在丢包时的行为决定的。

---

## 支持的平台

| 角色 | 支持 | 说明 |
|---|---|---|
| 服务器 | Ubuntu / Debian / Alibaba Cloud Linux / AlmaLinux / Rocky / CentOS / Alpine / Arch | 需要 systemd |
| 客户端 | Ubuntu 22.04+（含无影云电脑）、Debian 12+ | 需要 systemd + sudo |
| 操作端 | Linux、macOS、Windows | 只用来「远程配置服务器」，不必是被代理的那台机器 |

服务器架构支持 `amd64`、`arm64`、`386`。

**macOS 和 Windows 目前只能当操作端**：它们没有 systemd，装不了 dukou 的代理服务。
要在这些系统上用代理，可以在 WSL / Linux 虚拟机里跑客户端，或者把代理指向局域网内另一台
已经装好的机器。

---

## 卸载

```bash
# 客户端
sudo systemctl disable --now dukou-client
sudo rm -f /etc/systemd/system/dukou-client.service /usr/local/bin/gost
sudo systemctl daemon-reload

# 服务端
sudo systemctl disable --now dukou
sudo rm -f /etc/systemd/system/dukou.service /usr/bin/gost /etc/sysctl.d/99-dukou.conf
sudo systemctl daemon-reload
```

`~/.bashrc` 里被写入的代理变量在 `# >>> dukou proxy >>>` 和 `# <<< dukou proxy <<<`
之间，整段删掉即可。

---

## 开发

```bash
go build ./...                       # 命令行
go build -tags gui ./desktop         # 桌面版（需要 GTK/WebKit 开发库）
go test ./...                        # 单元测试
```

客户端与服务端共用 `internal/` 下的同一份内核，桌面版只是套了一层界面。

### 测试分四层

| 层 | 内容 | 状态 |
|---|---|---|
| L0 | 单元测试（纯函数、渲染、判断逻辑） | 每次提交跑 |
| L1 | GitHub Actions 容器矩阵：ubuntu / debian / almalinux / alpine / arch | 每次提交跑 |
| L2 | 临时按量服务器真机端到端 | 已在新加坡实例上跑通 |
| L3 | 生产灰度 | 待真实用户 |

## License

MIT
