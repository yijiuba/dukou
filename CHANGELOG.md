# Changelog

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循[语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### 已知缺口（下一个版本要解决）

- 独立 `client` 子命令是半成品（只到第 3 步），完整逻辑在 `deploy --client-only`；两条入口并存，需补完或删除其一
- `rotate-password` / `selfcheck` / `uninstall` 为占位命令，调用即报错（`internal/manifest` 已实现，待接线）
- PAC 自动分流未实现（仅打印计划）

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
