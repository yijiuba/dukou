# Changelog

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循[语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### 新增

- **AI 工具接真**：`internal/aitool` 提供 Codex / Claude Code 的检测、安装、启动、卸载。
  检测读 PATH 与真实版本号；安装先验 npm 源可达性再 `npm install -g`（经本机代理，
  `NO_PROXY` 保留国内直连），装完复查命令是否可用，PATH 不在时指出 npm 全局目录；
  卸载只卸 npm 全局包，命令仍在 PATH 就如实说明「另有副本，本工具不删它」；
  启动生成带代理环境变量的脚本并在新终端窗口运行。进度经 `ai:progress` 事件推送。
- **多服务器切换可测**：本机代理操作抽成 `proxyDriver` 接口（生产实现转调 `internal/proxy`，
  单测注入 fake），补 6 条单测覆盖「断旧→连新」顺序、断开失败中止、目标不存在、状态收口。

### 修复

- **切换线路把用户断网**：目标服务器已被删除时，原实现先断开当前线路再报错，
  用户从「连着」变成「断了且没连上」。改为先校验目标存在，失败时保留当前线路。
- **两台同时显示「使用中」**：代理状态快照丢失（手工删除、旧版本遗留）时旧服务器会永远停在
  `active`。连接成功后统一清理其它 active 标记，保证同一时刻至多一台在用。
- **AI 工具页的假状态**：删掉硬编码的「已安装 v0.1.4」「Windows 11 · 桌面版 v1.2.0 已安装 ✓」
  与 1.9 秒后假报安装完成的演示实现；ChatGPT 桌面版如实标注「厂商分发，不代装」。

### 已知缺口（下一个版本要解决）

- 独立 `client` 子命令是半成品（只到第 3 步），完整逻辑在 `deploy --client-only`；两条入口并存，需补完或删除其一
- `rotate-password` / `selfcheck` / `uninstall` 为占位命令，调用即报错（`internal/manifest` 已实现，待接线）
- macOS / Linux 的本机代理尚未实现：连接能力目前仅 Windows 可用，其余平台明确报错
- 自动备份、开机自启、自动下载安装更新未实现（只做提醒与跳转）

## [0.2.0] - 2026-11-16

v0.2「产品化基座」：把「部署 → 连接 → 体检」做成真正可用的桌面产品，
其余页面用可切换的界面数据先把流程跑通。

### 新增

- **本机代理（Windows）**：随包分发 gost 子进程，接管 `HKCU\...\Internet Settings` 系统代理；
  连接前把原始代理状态快照存盘，断开时逐项还原（含原本没有、原本是别的值的两种情况）
- **残留检测与一键清理**：进程被强杀/断电后重启，检测到"接管痕迹但代理进程不在"，
  首页给醒目横条，一键按快照还原——这类工具最招骂的故障
- **PAC 分流**：`国内域名/内网 → DIRECT，其余 → 代理`，用 ES3 语法书写以兼容 WinINet 老 JScript；
  同时写固定代理兜底，.NET 这类不加载 file:// PAC 的运行时不会断网
- **七层体检接 GUI**：环境/配置/服务/链路/鉴权/转发/性能（+分流），
  分数 = 100 − 20×fail − 8×warn，skip 不计；非当前线路只跑 L1~L4 并说明"需切到该线路"
- **多服务器清单**：`servers.json` + 加密凭据库，已连接时切线路走"断旧连新"，不留残留
- **本地持久化**：原子写（临时文件 + fsync + rename）、凭据 AES-GCM 加密且密钥由机器码派生、
  清单文件内绝不明文出现密码/私钥
- **`.dukoubak` 加密备份/恢复**：口令经 PBKDF2-SHA256（21 万轮）派生密钥、AES-256-GCM 整包加密；
  导入按服务器 ID 合并，导入前自动留 `pre-import-*` 快照
- **脱敏清单导出**：CSV（Excel 直开不乱码）/ JSON，密码一律 `******`，用于贴工单
- **更新提醒**：读 GitHub Releases 最新正式版，按 semver 比较（含预发布），提示 + 跳转下载
- **到期锁定**：仅保留 1 台服务器可切换，其余只读不可删不可编辑（服务器页有演示入口）
- **系统托盘**：关窗不退出，托盘菜单唤回主界面 / 退出

### 修复

- **退出不断开代理**：桌面层此前没接 `OnShutdown`，从托盘正常退出后系统代理仍指向
  已经关掉的 `127.0.0.1:7890`，浏览器全站打不开——现在退出先还原系统代理
- **点「立即备份」填完密码应用闪退**：Wails v2.10.1 的文件对话框（COM IFileSaveDialog）
  与 WebView2 组合存在崩溃——对话框弹出时主窗口收到 WM_ACTIVATE，WebView2 Focus
  回调返回 E_INVALIDARG，进程整体退出。改用系统记事本同款的 comdlg32 实现
  （`internal/winfile`），并修掉三个连带缺陷：跨线程 owner 未定义行为、
  桥接线程是 MTA 而 Explorer 对话框要求 STA、filter 含内嵌 NUL 时
  `UTF16FromString` 报 EINVAL
- gost 版本号无 `v` 前缀导致误判"未安装"，每次连接重下 17 MB
- 没有快照时 `Stop` 会删掉用户原有的代理配置；改成无快照直接返回
- 重复连接把自己写入的 `7890` 当成"原值"，最终还原到一个谁也没配过的状态
- `proxyOptions` 未归一化导致端口为 0 时探测 `127.0.0.1:0` 全部失败

### 变更

- 桌面窗口 1180×780（最小 1100×660），原型脱壳为九页产品界面
- **未实现的入口一律置灰并写明版本**：AccessKey 自动开通 → v0.4；同步配置、抢先版通道、
  迁移码、自动备份 → v0.3。宁可显示"暂未开放"，也不留点了没反应的按钮
- 首页版本条、服务器台数、出口 IP、延迟等全部改为真数据，删除演示文案

### 已知缺口

- 账号 / 订阅 / 订单 / 推广仍为界面演示数据（服务端契约 v1.0 已就绪，v0.3 接真）
- CLI 的 `rotate-password` / `selfcheck` / `uninstall` 仍为占位命令

## [0.1.0] - 2026-11-13

首个可用版本：一台海外服务器 + 一台国内机器，一条命令完成出网通道与 Codex 安装。已在新加坡实例（Alibaba Cloud Linux 4）与无影云电脑（Ubuntu 24.04）上端到端验证。

### 新增

- `deploy` 一键流水线：connect → server → client → codex 四步，每步「检查 → 执行 → 自检」，支持 `--server-only` / `--client-only` / `--codex`
- `server`：多发行版探测（debian/rhel/alpine/arch/suse 五家族）、gost 三通道安装（deb/rpm/静态二进制，含魔数校验）、systemd 托管、firewalld/ufw 放行、端口冲突检测
- `doctor` 七层分层诊断，每层失败即给修复命令；判断逻辑为纯函数，可单测
- 内核调优（BBR）：写 sysctl.d + modprobe + 回读确认；实测丢包链路下 10 KB/s → 210 KB/s
- 客户端引导借道服务器下载（`--proxy-insecure`），解决"没代理却要下 GitHub"的鸡生蛋问题
- Wails 桌面版：五个输入项一键部署，与 CLI 共用同一内核
- CI：unit×3 + build×3 + smoke×5（ubuntu/debian/almalinux/alpine/arch）+ desktop×2，共 14 个检查项
- 打 tag 即发版（`v*` tag 触发 Release workflow）：三架构 CLI + 两平台桌面版自动挂 GitHub Releases，附 sha256 校验和，无需登录即可下载

### 修复（均有回归测试）

- 执行层输出丢失：同一 buffer 并发写导致远端命令偶发返回空、误判"不是 root"
- 假成功：旧服务占用 7890 时新服务启动失败仍报全绿；新增 `is-active` 校验、端口属主识别、出口 IP 比对告警
- sudo 与重定向互斥导致配置未写入；统一改走 SUDO_ASKPASS
- 重跑幂等：识别自己装的 systemd 单元并覆盖（`.service` 后缀归一化比较）
- 运行中改密码不生效：`enable --now` 改为显式 `restart`

### 文档

- README 重写为「一台海外服务器」的保姆级上手流程
- 新增排障手册（docs/troubleshooting.md）：三条自查命令 + 十个真实故障
- 新增设计文档（docs/design.md）：全部决策与踩坑来源
