# dukou 架构与设计决策

> **这份文档解决什么问题**：记录每个设计决策"为什么是这样"，尤其是踩过的坑。
> 代码告诉你"做了什么"，这份文档告诉你"为什么不能改成别的样子"。
>
> **谁该读**：要改 dukou 代码的人。只想用它，看 [README](../README.md) 和 [排障手册](troubleshooting.md)。
>
> **维护约定**：
> - 新增一个设计决策，先在"工程原则"里加一条，格式为「规则 + *来源：真实故障*」。
> - 没有真实故障支撑的偏好，不写进来。
> - 实现状态改变时，同步更新[第 4 节](#4-实现状态设计-vs-代码)，它是文档与代码之间的对账表。

## 目录

1. [命名](#1-命名)
2. [定位与形态](#2-定位与形态)
3. [子命令](#3-子命令)
4. [实现状态（设计 vs 代码）](#4-实现状态设计-vs-代码)
5. [工程原则（踩坑 → 规则）](#5-工程原则踩坑--规则)
6. [服务端模块](#6-服务端模块server在出口服务器上执行)
7. [客户端模块](#7-客户端模块client在受控机器上执行)
8. [多操作系统适配层](#8-多操作系统适配层)
9. [诊断模块](#9-诊断模块doctor分层测试链)
10. [维护模块](#10-维护模块)
11. [路线图](#11-路线图)
12. [测试与验收策略](#12-测试与验收策略)
13. [已知边界](#13-已知边界)

---

## 1. 命名

| 项 | 值 |
|---|---|
| 命令名 / 仓库名 | **dukou** |
| 中文品牌 | **渡口** |
| 完整描述 | dukou —— 国内机器一键接入海外大模型（运行网络 + 自动化安装） |
| 子命令读法 | `dukou server` / `dukou client` / `dukou doctor` |

**为什么叫渡口**：渡口是此岸到彼岸的登船点。一台出不了网的机器，从这里登船，到对岸去。这个意象精确对应工具的职责——它不生产流量，只提供一条可重复使用的过河通道，并把上船所需的一切（通道、分流、CLI、自检）一次办齐。

**为什么用拼音而不是英文**：目标用户是国内开发者，「渡口」两个字不需要解释。反过来，任何「ai + 英文短词」的组合都已被 AI 热潮占满（见否决记录），硬挤进去只会不断撞名。

**两层命名**：

- 品牌层（被搜到、一眼懂）：**渡口** / 「dukou —— 国内机器一键接入海外大模型（运行网络 + 自动化安装）」
- 命令层（每天敲）：**dukou**，5 个字母，无连字符，不用按 Shift

**为什么名字里不出现具体产品名**：目标是「接入海外大模型」，Codex 只是第一个 profile。用 `--profile codex|claude|gemini` 扩展，名字不随产品变化。

**否决记录**（每条均已实际查证）

| 候选 | 否决原因 |
|---|---|
| `cnai` | 读作「中国 AI」，与实际用途（给国内机器开通道）正好相反，歧义不可接受 |
| `chuhai` / 「AI 出海」 | 「出海」在国内已被「品牌出海 / 独立开发者出海」占用（`chuhai.tools` 是独立开发者出海工具库，npm 也有 `chuhai` 包），用户会误以为这是电商出海工具 |
| `ferry` | GitHub 已有同名同功能项目（把 codex/claude/pi/cursor-agent 同步部署到远程 Linux，子命令风格几乎一致） |
| `aiport` | GitHub `harelix/aiport`，Go 写的本地 LLM 网关，连 `aiport agent codex` 用法都一致 |
| `aipass` | GitHub / PyPI `AIOSAI/AIPass`，连 `aipass doctor` 子命令都一样 |
| `aiship` / `ai-ship` | npm `ai-ship-cli`、`ship-skills` 的 CLI 命令即 `ai-ship` |
| `aiway` | GitHub `xgpxg/aiway`，Rust 写的「API + AI 融合网关」，中文团队作品，重叠度极高 |
| `aivia` | npm 有同名包（2019 年废弃）、GitHub 有同名 Claude Code 插件，搜索结果会互相干扰 |
| `egress` / `ai-egress` | npm `@absolutejs/egress`、GitHub `tailscale-ai-egress` 已占；更严重的是 `Easegress`（CNCF 沙盒项目，主打 AI Gateway）仅差三个字母 |
| `oversea` | 拼写不规范（正确为 overseas），npm 另有 `oversea-vue`，语义泛化无法承载工具定位 |
| `cn-egress-aiCIL` | `CIL` 应为 `CLI`；三个连字符每次输入要按三次 Shift；语序应为「主体（AI）+ 手段（egress）」 |

**一条可复用的结论**：`ai-` 前缀 + 常见英文短词的命名空间已经饱和（`aiport` / `aipass` / `aiship` / `aiway` / `aivia` 全部撞车）。新工具必须避开这个词库。

## 2. 定位与形态

| 项 | 决策 | 理由 |
|---|---|---|
| 形态 | **Go 单静态二进制**（v2 修订，否决了初版 Bash 方案） | 保留「零依赖、单文件、开箱即用」，同时获得内置单元测试、交叉编译（Win/Mac/arm64 免费覆盖 client-win）。通用工具的分支逻辑必须有测试兜底，Bash 做不到 |
| 范围 | 通用工具（别人也能用） | 必须处理未知环境：不同发行版、端口冲突、已有代理、arm64 |
| 诊断 | v0.2 起内置 doctor | 排查最耗时的部分可全部编码为决策树，收益最大 |
| 交互形态 | 命令行 + Wails 桌面窗口（同一份内核） | 目标用户未必想碰命令行，桌面版把五个输入项收进一个窗口；CLI 仍是可脚本化的正式入口 |

**它不是代理软件，是"安装器 + 自检器"**：代理能力全部来自 gost，dukou 负责把 gost 装对、配好、验证通过，并在出问题时告诉你在哪一层断了。这个边界决定了「它不做什么」（见第 13 节）。

## 3. 子命令

```
dukou deploy --server IP --ssh-password X    # 一键：远程装服务端 + 装本机客户端（桌面版就调这条）
dukou server              # 服务端部署（在出口服务器上跑）
dukou client --server IP  # 客户端部署（在受控机器上跑）
dukou doctor              # 分层诊断（两端各自跑，输出断点+修复命令）
dukou rotate-password     # 生成随机密码并同步两端
dukou selfcheck           # 快速自检（服务状态/BBR/分流/CLI）
dukou uninstall           # 按 manifest 清单精确回滚

# 全局开关
--dry-run        # 打印完整执行计划，不改任何系统状态
--takeover       # 显式授权接管被占用的端口（如 xray），替代交互询问
--port N         # 显式指定端口避让
--profile NAME   # 目标 CLI：codex | claude | gemini（v0.1 仅 codex）
```

命名说明：诊断子命令叫 `doctor` 而不是 `diagnose`——`dukou doctor` 是这类工具（brew doctor / flutter doctor）的通行惯例，用户觉得"不对劲"时的第一反应就是敲 doctor，且少两个字母。

## 4. 实现状态（设计 vs 代码）

**每次发版前对账这张表。** 它防止两件事：文档承诺了代码没有的能力；功能做完了却没人知道可以对外说。

| 子命令 / 模块 | 状态 | 说明 |
|---|---|---|
| `deploy` | ✅ 已完成，真机验证 | 四步流水线：connect → server → client → codex，每步「检查 → 执行 → 自检」。`--server-only` / `--client-only` / `--codex` 可用 |
| `server` | ✅ 已完成，真机验证 | 环境探测、端口冲突、gost 三通道安装、systemd、防火墙、BBR |
| `client`（独立子命令） | ⚠️ 半成品 | 只实现到第 3 步（dry-run 可看完整计划）。**真正可用的客户端逻辑在 `deploy --client-only` 里** |
| `doctor` | ✅ 已完成 | 七层诊断，逐层失败即停并给修复命令 |
| `rotate-password` | ❌ 占位 | 调用 `notImplemented`，明确报错而非静默成功 |
| `selfcheck` | ❌ 占位 | 同上 |
| `uninstall` | ❌ 占位 | `internal/manifest` 已实现且有测试，但命令未接线；README 的卸载章节目前给的是手工命令 |
| PAC 自动分流 | ❌ 未实现 | `client` 只打印"本地托管 PAC 于 8899"的计划，未真正生成与托管 |
| `internal/systune`（BBR） | ✅ 已完成 | 写 sysctl.d + modprobe + **回读确认**，回读不到即报错 |
| 桌面版（Wails） | ✅ 可用 | 五个输入项，一键走 `deploy` 同一条流水线 |
| CI | ✅ 14 个 job 全绿 | unit×3 / build×3 / smoke×5 / desktop×2 / ci-ok |

**已知交叉影响**：`client` 与 `deploy --client-only` 是两条入口，前者半成品、后者完整。新增客户端能力时**只改 `internal/deploy/client.go`**，别在 `cmd/client.go` 里平行开发，否则立刻产生两份实现。这个子命令要么补完、要么在 v1.0 前删掉。

## 5. 工程原则（踩坑 → 规则）

1. **单一配置源**：所有可变项（server IP、user、password、port）集中到 `/etc/dukou.conf`，任何组件只引用变量。
   *来源：两端密码差一个字符（f vs 6），绕了三轮 407。*
2. **原子模板写入，禁止 sed 补丁**：改配置一律「生成 .tmp → 语法校验 → mv 覆盖」，改完 `grep` 核对关键行。
   *来源：sed 模式含不存在的字符串时静默跳过，restart 照样成功，跑的还是旧配置。*
3. **幂等可重入**：每个动作先检测现状再执行，脚本重跑不产生副作用、不重复安装。
4. **每步验证门**：安装类步骤完成后立刻验证（端口监听、带凭据 curl），失败即停并打印该失败的修复命令，不继续往下走。
5. **修改前备份**：接管已有服务（如 xray）前先打包配置到 `/root/backup/`，随时可回退。
6. **默认安全**：密码自动生成 16 位随机串；TLS 加密口（8443）为主口；明文口仅限需要时开启。
7. **诊断用 `curl -sS`**，禁止裸 `-s`。
   *来源：`-s` 把 407 吞成空白 + 000，被误判为网络故障。*
8. **manifest 清单制**：工具的每一处系统变更（写入的文件、创建的服务、加的防火墙规则、改的 sysctl）实时记录到 `/var/lib/dukou/manifest.json`，uninstall 按清单精确回滚，不做反向猜测。
9. **非交互优先**：所有「需要决策」的点（端口冲突、已有服务接管）默认给出安全结论并停止，仅在显式传 `--takeover` / `--port` 时自动执行；`--dry-run` 永远可先看计划再动手。
10. **"命令返回 0"不等于"操作生效"**：每个写操作之后必须回读校验（`is-active`、端口属主、`sysctl -n`），校验不过一律当失败处理。
    *来源：手工 gost-client 占着 7890，新服务 bind 失败退出，`systemctl restart` 仍返回 0，自检连到旧代理后全绿，出口 IP 却是上一台服务器。*
11. **凡是用 `sudo` 的写操作，不要靠 shell 重定向**：`sudo echo x > /etc/...` 里 `>` 由调用者 shell 解释，文件根本写不进去。统一走 `sudo tee` 或 SUDO_ASKPASS。
    *来源：改 `tee` 后 stdin 又被密码管道占走，最终改用 SUDO_ASKPASS。*
12. **识别"自己的痕迹"要比对归一化后的名字**：systemd 报的是 `xxx.service`，代码里存的是 `xxx`，直接比较永不相等。
    *来源：重跑时把自己装的 `dukou-client.service` 判成"别人占端口"，工具拦住了自己。*

## 6. 服务端模块（`server`，在出口服务器上执行）

1. **环境探测**：发行版（dnf/apt/zypper）、架构（amd64/arm64）、是否 root、gost 是否已装。
2. **端口冲突处理**：`ss` 检查 8443/1080/1081；发现占用（xray/x-ui 等）→ **默认停止**并打印占用者与两条出路（`--takeover` 接管 / `--port` 换口）。
   *来源：1080 被 xray 占用导致 bind 失败。*
3. **安装 gost**：按架构从 GitHub release 下载 rpm/deb/二进制，包文件做魔数校验（避免拿到 HTML 错误页当安装包）；下载失败时提示从客户端传包的路径。
4. **systemd 单元**（模板写入）：
   - 主口 `http+tls://user:pass@:8443`（gost 自动生成自签证书）
   - 备用口 1080（HTTP）/ 1081（SOCKS5），默认仅按需开启
   - `Restart=always`
5. **防火墙三层放行**：firewalld/ufw 自动处理；阿里云控制台安全组无法自动化 → 输出精确的操作指引 + 一条「从外部探测端口」的自检命令。
   *来源：漏开任何一层，症状都是无报错的超时。*
6. **内核调优**：modprobe tcp_bbr + `modules-load.d` 持久化 + fq qdisc + 缓冲区按 BDP 显式放大（64MB），写完回读确认。
   *来源：Alibaba Cloud Linux 8 内核有 tcp_bbr.ko 但默认不加载；10% 丢包链路下 CUBIC 只有 27KB/s，BBR 后 1.65MB/s。*
7. **服务端自测（出口验证门）**：带凭据 curl → 期望返回本机公网 IP；`api.openai.com` 返回 401 判定链路通（401 说明请求到达了 OpenAI，只是没带 key）。

## 7. 客户端模块（`client`，在受控机器上执行）

1. **环境探测**：Ubuntu 版本、arch、是否有桌面（`$XDG_CURRENT_DESKTOP`）、Firefox 是否为 snap。
2. **先装本地中转**：gost-client systemd 服务，`7890(HTTP)/7891(SOCKS5)` 本地无鉴权 → 上游 `tls://server:8443`。
   架构：`应用 → 127.0.0.1:7890 → tls → 出口服务器:8443 → 海外`。密码只出现在这一处。
3. **连通性验证门**：`curl -x http://127.0.0.1:7890 https://api.ipify.org` 必须返回服务器 IP，否则停在这里（问题必在服务端/安全组）。
4. **环境变量三处落盘**：`/etc/environment` + `/etc/profile.d/proxy.sh` + `~/.config/environment.d/95-proxy.conf`。
   *来源：从应用菜单启动的 GUI 程序不读 profile.d，只读 environment.d。*
   `NO_PROXY` 必须含 localhost 与内网段。
5. **桌面代理**（有桌面才做）：gsettings manual + `ignore-hosts` 含 localhost（Codex 登录回调 localhost:1455 的坑）。
6. **PAC 分流**：生成「国内域名直连 + 其余走代理」的 PAC，由本地 `pac-server`(8899) 托管。**（未实现，见第 4 节）**
   *来源：snap Firefox 因 AppArmor 读不到 `file://` 的 PAC；扩展类方案（ZeroOmega）在 snap Firefox 下无法接管代理。*
7. **apt/snap 按源分流**：默认 DIRECT，仅白名单源（dl.google.com 等）走代理。
   *来源：全量代理把阿里云内网镜像 `mirrors.cloud.aliyuncs.com` 送出国导致 503。*
8. **Node 22 + Codex**：NodeSource 装 v22（Ubuntu 仓库自带 18 不够）；npm 可选国内镜像；登录引导——检测桌面环境，浏览器流程失败时回退 API Key 方式。
   *来源：Firefox ETP 拦截 `challenges.cloudflare.com` 导致授权无限转圈，需关站点级跟踪保护。*
9. **终验**：`api.ipify.org` = 服务器 IP、google/chatgpt 200、`codex --version` 正常。
10. **引导自身需要网络（鸡生蛋）**：本机还没代理却要从 GitHub 下东西时，先借道已装好的服务器（`curl -x https://server:8443 --proxy-insecure`），失败再回退直连。

## 8. 多操作系统适配层

任何 OS 差异都收敛到一个模式：**探测 → 分发 → 兜底**。每个能力点都定义「首选实现 → 等价实现 → 通用兜底」三级，兜底保证任何 Linux 至少有一条完整通路。

### 8.1 两条铁律（先于适配表）

1. **gost 永远用静态二进制兜底**。rpm/deb 只是便捷通道，任何发行版最终都可退到「下载 tar.gz → 解压到 `/usr/local/bin` → 手写 init 单元」。gost 是 Go 静态编译，glibc/musl 通吃。
2. **PAC 一律走 `http://127.0.0.1:8899` 本地托管**，不做 `file://` 直读。这样 snap / flatpak / rpm / deb 形态的浏览器全部通吃，不存在 AppArmor 沙盒差异问题。Ubuntu 24.04 的坑降级为「pac-server 必装」这一条统一规则。

### 8.2 服务端适配矩阵

探测项：发行版 → 包管理器 → init 系统 → 防火墙 → 内核版本 → SELinux 状态。

| 能力点 | 首选 | 等价 | 通用兜底 |
|---|---|---|---|
| gost 安装 | dnf/rpm、apt/deb | zypper、pacman | **静态二进制 + /usr/local/bin**（所有发行版） |
| 服务托管 | systemd | OpenRC（Alpine）、SysV | cron `@reboot` + nohup（标记为降级） |
| 防火墙 | firewalld（RHEL 系） | ufw（Ubuntu/Debian） | nftables / iptables 直接加规则；无防火墙则跳过并提示 |
| BBR | 内核 ≥4.9：modprobe + sysctl | 内置已启用（Ubuntu 24.04 等新内核） | **无 BBR 的老内核（CentOS 7 的 3.10）**：不装内核，改推 Hysteria2（用户态 QUIC，不依赖内核拥塞算法） |
| SELinux（RHEL 系） | 非 enforcing 则忽略 | enforcing：`semanage port -a -t http_port_t -p tcp 8443` | 提示手动处理并给出命令 |

### 8.3 客户端适配矩阵

探测项：发行版 → 桌面环境（`$XDG_CURRENT_DESKTOP`）→ 浏览器安装形态 → init 系统。

| 能力点 | 首选 | 等价 | 通用兜底 |
|---|---|---|---|
| gost 安装 | apt/deb | rpm | 静态二进制（同服务端） |
| 环境变量三处 | `/etc/environment` + `profile.d` + `environment.d` | — | 三处是 XDG/systemd 用户级标准，GNOME/KDE/Wayland 通用，不随发行版变 |
| 桌面代理 | GNOME：gsettings | KDE：提示在系统设置 GUI 手动配（命令行写 kdeglobals 不可靠） | XFCE/未知桌面：跳过 gsettings，仅靠环境变量 + PAC |
| 浏览器分流 | PAC over HTTP 8899（全形态通用） | — | 无浏览器则跳过 |
| apt/snap 分流 | apt conf + `snap set system proxy` | dnf.conf / pacman 无需（它们认环境变量） | 无包管理器则跳过 |
| Node 22 | NodeSource（deb/rpm 双通道） | — | **nvm 安装**：无需 root、任何发行版可用（含 Alpine musl，需 glibc 兼容层时改用官方静态构建） |
| 服务托管 | systemd user/system 单元 | OpenRC | cron @reboot（降级标记） |

### 8.4 实现方式

探测结果写入 conf（`OS_PROFILE=deb|rpm|binary`、`INIT=systemd|openrc|cron`、`FW=firewalld|ufw|nft|none`、`DESKTOP=gnome|kde|other|none`、`ARCH=amd64|arm64`），所有后续模块只看这五个变量分支，不直接调用 `if lsb_release...`。新增发行版 = 新增一行 profile 映射，不动模块逻辑。

代码里对应 `internal/detect` 的 `familyOf()`：一张表把发行版 ID 收敛到 debian / rhel / alpine / arch / suse 五个家族，其余一律走 binary 兜底。

## 9. 诊断模块（`doctor`，分层测试链）

按层递进，每层失败即输出「结论 + 修复命令」，不继续下层。默认只诊断；`--fix` 才自动执行修复。

| 层 | 测试 | 失败含义 → 修复 |
|---|---|---|
| L1 链路 | 直连 `服务器:8443` TLS 可达 | 安全组/防火墙 → 放行指引 |
| L2 鉴权 | 带凭据走代理 curl | 407 → 两端 `grep ExecStart` 比对密码 |
| L3 转发 | github.com 经代理 | 503/reset → 看 journalctl，检查 gost 上游 |
| L4 域名 | github 通但 google/chatgpt 不通 | 明文 CONNECT 被识别 → 确认 `-F tls://` |
| L5 性能 | Cloudflare 下载测速 + ping 丢包 + mtr 最后一跳 | 吞吐公式 `(MSS/RTT)×(1/√丢包)` 反算 → 判定带宽/丢包/BBR 未生效 |

*这条分层链是完整复盘：每一层都真实对应一次误诊（000≠断网、407≠网络问题、mtr 中间跳丢包是假象、MTU 全通排除分片）。*

`internal/diagnose` 把判断逻辑做成**纯函数**（`ClassifyProxyResult` / `AnalyzeDomainSpecific` / `AnalyzeThroughput`），不碰网络、不做 IO，因此每条误诊教训都能写成单元测试。

## 10. 维护模块

- **rotate-password**：`openssl rand` 生成 → 原子更新两端 conf 与 systemd → 双端 restart → 走代理 curl 验证。（当前为占位）
- **selfcheck**：gost/gost-client/pac-server 三服务 active + `tcp_congestion_control=bbr` + PAC HTTP 200 + `codex --version`。（当前为占位）
- **uninstall**：逆序清理 systemd 单元、防火墙规则、环境变量、PAC（保留 `/root/backup/`）。（当前为占位）

> 占位期间，README 的卸载章节给的是等价的**手工命令**，逐条对应上面三件事。接线时要保证手工命令与 manifest 回滚结果一致。

## 11. 路线图

| 版本 | 内容 | 备注 |
|---|---|---|
| v0.1 | Go 骨架 + server + client 主路径（Debian/Ubuntu + RHEL 系），每步验证门 | ✅ 已完成并在新加坡实例上真机跑通 |
| v0.2 | 桌面版 + `deploy` 一键流水线 | ✅ 已完成（原计划的 doctor 已提前落地） |
| v0.3 | doctor 分层诊断 + rotate-password | ⏳ doctor 已完成，rotate-password 待接线 |
| v0.4 | 适配层落地：rpm/deb/二进制三通道、firewalld/ufw/nft、arm64、无桌面降级 | ⏳ 三通道已在 v0.1 实现，其余待补 |
| v0.5 | uninstall（manifest 回滚）/ selfcheck、Alpine（OpenRC）、CentOS 7（Hysteria2 通道） | |
| v1.0 | 独立 `client` 子命令补完或删除、PAC 自动分流、Windows/macOS 客户端（`client-win`） | Go 交叉编译直接产出 |
| v1.1 | Hysteria2 可选传输 | 仅在 L5 判定拥塞算法无效时推荐 |

## 12. 测试与验收策略

### L0 单元测试（不需要用户）

Go 内置 testing。覆盖：OS 探测解析、profile 分发、conf 读写与原子写入、manifest 一致性、doctor 决策树（把全部误诊场景做成测试用例：000≠断网、407=密码差一位、mtr 中间跳假丢包、github 通但 google 不通→L4）。

**每个真实 bug 必须先补一条会失败的测试，再改代码。** 这不是洁癖：本项目 6 个真实缺陷（并发写 buffer、假成功、sudo 与重定向、sameUnit 后缀等）全部是"修完没有测试就会再犯"的类型。

### L1 容器矩阵（GitHub Actions，不需要用户日常参与）

本机无 Docker、WSL 被安全策略拦截 → 矩阵跑在 CI 上：

- 矩阵：`ubuntu:24.04` / `debian:12` / `almalinux:9` / `alpine:3.20` / `archlinux` × amd64
- 只跑只读操作（`version` / `doctor` / `--dry-run` / `help`），不装任何东西
- 真正目的：证明「静态二进制兜底」成立——Alpine 用 musl libc，glibc 编译的程序在那里根本起不来

### L2 真机集成（临时按量服务器，约 0.1~0.5 元/小时）

容器覆盖不了的东西在真机验：真实 systemd、firewalld、云厂商安全组、真实公网链路。流程：

1. 创建按量付费实例（建议海外节点，便于测真实出口）
2. 真机跑 `server` + 从任意外部机器跑 `client --server <IP>` 全流程
3. `doctor` 全绿 + 卸载无残留
4. 跑完销毁实例

### L3 验收（真实生产环境灰度）

在真实云电脑 + 生产服务器上线。工具对「手工配置残留」的检测与兼容是 L3 专项用例——机器上可能已有手工装的 gost/pac-server/BBR，工具必须能识别、收编或提示先卸载手工版。

### 各层失败的处理约定

任何一层发现的问题必须先在 L0 补一条对应测试用例再修复（回归保险），然后逐层重跑受影响的层。

## 13. 已知边界

工具做不到的事，明确写进输出提示，不做"假装成功"：

- 阿里云控制台安全组规则：只能给指引 + 提供外部探测自检命令。
- Cloudflare 对机房 IP 的风控：客户端无解，只能提示换 IP / 换地域。
- 透明代理（iptables 全局）风险高（漏 RETURN 会整机断网），不纳入第一版。
- UDP（QUIC/HTTP3）绕过代理：提示浏览器禁 QUIC / 关 HTTP3。
- 国内 ISP 国际出口带宽：3 路并行仍只有 ~242KB/s 时，瓶颈已不在软件，工具只能给出这个结论而不是继续优化。
