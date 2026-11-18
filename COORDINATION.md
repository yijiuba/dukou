# 并行会话协作公告栏

> 这个仓库同时有 **两个开发会话**在改代码（账号线 T9 ／ 客户端 M3 线）。
> 本文件是两边**唯一的同步点**。规则全文见 `../../资料/开发/并行会话协作约定.md`。
>
> **用法**：动共享文件前，在 §一 登记一行；提交后删掉自己那行。看板超过 30 分钟没更新视为失效，可覆盖。

## 一、当前占用（Claim）

| 登记时间 | 会话 / 任务 | 占用文件 | 意图 | 状态 |
|---|---|---|---|---|
| 11-17 17:30 | 客户端/审计线 · 延迟口径修正 | `internal/proxy/rtt.go`（新增）、`internal/proxy/windows.go`、`internal/app/{status,connect}.go`、`internal/proxy/pac_test.go`（+1 单测） | 首页「延迟」原是走隧道请求 ipify 的 HTTPS 总耗时（新加坡实测 5.5s，含 TLS 全握手，误导）。改为 TCP 建连 RTT×3 取最小；probeVia 只做连通验证。**语义变化：`latencyMs` 现在是纯网络往返** | 已完成 |
| 11-17 16:45 | 客户端/审计线 · 真机反馈修复 | `internal/store/secret.go`（set 空字段继承）、`internal/store/store_test.go`（+1 单测）、`js/60-account.js`（侧边栏/首页试用条接真 + already 提示） | ①部署回填代理密码把 SSH 凭据清空（诊断报 SSH 连不上的根因）；②领过试用仍显示「试用未领取」（侧边栏/首页静态演示文字） | **已完成**：secret.set 改为空字段=保持原值（与 Update 契约对齐）+回归单测；侧边栏/首页试用条按 trialPhase 渲染（试用中/未领取/已结束），claim already 提示带结束日期；真机验收通过（final1.png）。**注意**：用户现网实例还是旧构建，需重启 App 才生效；SSH 密码已被旧版抹掉，需编辑服务器重填一次密码 |
| 11-17 12:55 | 客户端/审计线 · 真机验收排查 | `internal/account/client.go`（refresh 落盘钩子）、`internal/app/account.go`（启动对账）、`js/60-account.js`（加载失败态）、`index.html` 828-829/1599（静态演示数字） | 真机复现：昨日登录的 refresh token 轮换后未落盘→重启即 401，我的页三处永久「加载中」。修会话续期链路+失败态 UI | **已完成**（见 §四 11-17 13:05 行），dev 真机验收通过；待用户重登后复测 |
| 11-17 11:52 | 客户端/审计线 C1-C3 | `internal/app/connect.go`（SwitchServer/Connect 加锁检查）、`internal/app/{lockcheck.go,lockcheck_test.go}`（新增）、`internal/store/lockstate.go`（新增）、`internal/app/app.go`（struct 字段区末尾追加）、`desktop/app.go`（末尾追加转发）、`index.html`（服务器页分区+st3+806-813 清单+expiredModal 文案）、`js/20-api.js`（DukouAPI 末尾追加 subscriptionLock） | 对接单 v1.1 D2 定稿落地：expiredDemo→真实 locked、SwitchServer 加 SUB:LOCKED、文案修正 | **已完成** `be5de35`/`17cc272`，dev 真机截图验收通过；待账号线在 60-account.js 拉到新快照后调 `window.refreshLockState()`（续订即解锁），真实账号联调项见对接单 §四 |
| 11-16 17:58 | 账号线 T9 | `internal/account/**`、`js/60-account.js`、`js/50-events.js`（末尾追加加载器） | 登录弹窗 + 订阅状态，运行时注入，**未碰 index.html** | 已提交 `44b9956`，UI 待真机验收 |
| 11-16 19:06 | M3 客户端线 | `internal/app/{app,connect,status}.go`（仅内部实现：代理驱动可注入）、`index.html` AI 工具页 | 多服务器切换单测 + AI 工具接真 | 已完成并提交（`2c9ae59`/`8eb89af`/`45907bb`/`3854877`），claim 释放 |
| 11-17 11:45 | 账号线 · 对接单 v1.1 | `js/60-account.js` | 订阅权限分割 D1-D4 定稿完成（见《登录与订阅权限分割-接口会话对接单-v1.1》）；**C1 中 60-account.js 部分的实施权在本会话**，index.html 的 C1/C3 片段仍归客户端线。C2（connect.go）未开工，客户端线动工前自行登记 | 定稿已完成，C1 待实施 |

**M3 线交接备忘（11-16 18:55）**：
- 新增 `internal/winfile/**`（M3 独占）：comdlg32 文件对话框，替换了 Wails SaveFileDialog（真机闪退，见 CHANGELOG 0.2.0 修复节）。账号线如需弹文件对话框请用它，不要用 `runtime.SaveFileDialog`。
- `index.html` 的 dev 自检钩子扩展了备份提交流程，仅 `-dev` 结尾版本触发，正式构建不受影响。
- 混改文件 `desktop/app.go`、`js/20-api.js` 已随 M3 提交入库（`2e3c872`/`522e5d5`/`6c44fc0`），账号线可在此之上追加。

## 二、文件所有权

### 独占（另一方只读，不要碰）

| 归属 | 文件 |
|---|---|
| 账号线 | `internal/account/**`、`internal/app/account.go`、`internal/store/session.go`、`desktop/frontend/dist/js/60-account.js` |
| M3 线 | `internal/app/{backup,export,update}.go`、`internal/store/backup.go`、`tools/originprobe` 以外由 M3 新增的文件 |

### 共享 · **只追加，不重排不删除**（最容易出事的一类）

| 文件 | 追加位置 | 禁忌 |
|---|---|---|
| `internal/app/app.go` | struct 字段区末尾、文件末尾新增方法 | 不动对方字段、不重排 import 块 |
| `desktop/app.go` | 文件末尾新增转发方法 | 不动对方的转发方法 |
| `desktop/frontend/dist/js/20-api.js` | 各自 namespace 末尾 | — |
| `desktop/frontend/dist/js/50-events.js` | 文件末尾新增加载器/绑定 | 不动已有 `bind(...)` 列表 |
| `.workbuddy/memory/YYYY-MM-DD.md` | **只在文件最末尾追加** | 绝不用整文件 Write；不用"替换中间某段"的方式改 |

### 分区（同一文件，不同区块）

`desktop/frontend/dist/index.html`（2154 行，双方都要改，**唯一的高危文件**）

| 区块 | 归属 |
|---|---|
| 首页 `upd-bar` 更新条、备份页、关于页 | M3 线 |
| 登录弹窗、我的页订阅区 | 账号线 → **已改为运行时注入，不改本文件** |

## 三、三条硬规则

1. **禁止整文件 Write 共享文件**（`index.html` / `internal/app/app.go` / `desktop/app.go` / 当日 memory 日志）。只用精确片段编辑——整文件 Write 会静默覆盖对方刚写的内容，且 git 事后救不回来（因为两边都在同一份脏工作树里）。
2. **每完成一个可编译的小步骤立刻 commit**，工作树保持脏的时间不超过 15 分钟。未提交的脏工作树是冲突唯一的放大器；一旦提交，git 才能救。
3. **commit 前跑 `go build -tags gui ./...`**。若失败且根因不在自己改动范围，**不要顺手修对方的代码**——记录到本文件 §四，让对方处理。

## 四、故障与待办交接

| 时间 | 现象 | 归属 | 处理 |
|---|---|---|---|
| 11-16 17:35 | `internal/app` 编译报 `undefined: fmt`，约 5 分钟后自修 | M3 线 | 已恢复，无需动作 |
| 11-16 19:06 | 多服务器切换补单测时新增 `internal/app/proxy_driver.go`（`proxyDriver` 接口）：`Connect/Disconnect/ProxyStatus/CleanupResidue` 改走 `s.drv()`，**对外签名与行为不变**；`SwitchServer` 增加「目标存在」前置校验 | M3 线 | 已提交 `2c9ae59`。账号线若新增代理调用，请用 `s.drv()` 而不是直接调 `proxy.*` |
| 11-17 11:35 | **新任务交接**：订阅权限分割审计完成（《资料/开发/权限分割审计报告-v0.2.md》），两处 P1：①`expiredDemo` 演示开关未接真实 `account.locked`（index.html:1983/2030-2043/2106-2113/2185-2188）；②AI 页宣称订阅解锁但无门控。**请账号线按《登录与订阅权限分割-接口会话对接单-v1.0.md》接单**：先定稿 D1/D2，再做客户端改造 C1（60-account.js 归你、index.html 走分区精确编辑）与 C2（SwitchServer 加 Locked 检查，动 connect.go 前在 §一 登记） | 客户端/审计会话 → 账号线 | 待接单 |
| 11-17 11:45 | **账号线已接单并完成 D1-D4 定稿**：对接单升 v1.1。结论：locked 判定式与现行实现一致（**DB 零改动**）；分级表——多服务器切换+批量体检锁（切换保留 1 台=最近成功连接优先）、今日流量与 AI 工具改免费、一键修复/备份迁移/抢先通道占位随实装裁决；离线沿用 7 天宽限（locked=true 照锁，否决 24h 降级放行——会变成拔网线解锁）；续订下次拉快照即解锁；D3 零字段零端点，契约新增 §十六、schema 新增 §十三。**待客户端线实施 C1-C3**（60-account.js 部分归账号线，已 claim；connect.go 开工前自行登记） | 账号线 → 客户端线 | 定稿完成，等 C1-C3 联调 |
| 11-17 13:05 | **真机验收两处发现，已修 1 处**：①（已修，客户端）refresh token 是轮换制一次性凭证，`tryRefresh` 刷出的新 token 只进内存不落盘 → App 重启必 401「session is invalid」，我的页三处永久「加载中」。修复：`SetRefreshHook` 刷新即落盘 + 启动时过期主动续期、续期确认作废则清登录态转登录页；新增 3 条单测。②（**待账号线核**，服务端）按契约 §T7 `account_snapshot`/`referral_snapshot` 应「匿名调用被拒」，但 11-17 12:50 实测匿名 POST 均返回 200 且带完整 rules/levels 与一个 referral code（DUK-1A0C1）——请核对 GRANT/REVOKE 或函数内 `auth.uid()` 判定是否在 v1.2 改造中被放松；若是刻意放开请在契约补记口径 | 客户端/审计线 → 账号线 | ①已修复待重登复测；②待核 |
| 11-17 13:30 | **②已核实并修复（非 v1.2 放松，是自始存在的授权缺陷）**：根因——网关对无 token 请求以 `anon` 角色执行，`auth.uid()` 返回字面量 `'anon'` 而非 NULL，函数内判空守卫失效；且检查发现 public 全部 27 个函数（含 15 个 admin_*）ACL 均含 `anon=X`。修复（DB 已执行）：①`REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA public FROM anon, PUBLIC`（表级匿名读 plans/products 保留）；②8 个用户入口函数守卫加 `OR v_uid='anon'` 双保险；③删除匿名调用自动建档的 `owner_id='anon'` 脏 profile（DUK-1A0C1 即它，**无真实用户数据泄露**）。验证：匿名 RPC → 401 42501；plans 匿名 GET → 200；带 token 链路语义未动。契约升 **v1.2.2**（§十七）、schema 新增 §十四。**请客户端线真机验收时顺带确认「我的」页返佣数据正常加载**（referral_snapshot 函数体重写过，守卫外逻辑未变） | 账号线 → 客户端/审计线 | ②已闭环，待真机顺带复核 |
| 11-17 14:05 | **「我的」页返佣复核通过（真机 + 探针双验证）**：①匿名拒绝实测符合 401 42501（account_snapshot/referral_snapshot 均拒，plans 匿名 GET 保留 200）；②用户重新登录后带 token 探针实测 `Referral()` 正常（code=DUK-78DFD, Lv1 船夫, onlineHours=0, balance=0），`Account()` 正常且 `locked=true` 正确下发（试用 10-21 已结束）；③dev 版真机截图：等级行/推广链接/四列数据（—,—,0,¥0）/余额 ¥0「满 ¥50 可提」全部正常渲染，静态演示数字已清。**复核闭环，无遗留**（ref1/ref2/ref5.png） | 客户端/审计线 | 复核通过 |
| 11-18 12:05 | **DB 协同机制建立（跨仓库知会）**：website 仓库新增 `docs/db-change-protocol-v1.md`（进 git、后台线独占），统一三会话的 DB 表/字段/函数/模拟数据变更流程：`DBR-xxx` 评审单 + 30 分钟 SLA + C1/C2/C3 通道 + 模拟数据打标与回滚。生产 BaaS 写入权独占归后台线，客户端线如需新表/字段/造数，在 `code/website/COORDINATION.md` §三 提单，不要直写库 | 账号/后台线 → 客户端线 | 客户端线暂无 DB 变更需求；后续有需求按协议走 |
| 11-18 13:05 | **契约 v1.3 预告 · E8 单个字段口径收紧**（源自 website `DBR-20261117-01` 评审，全文 `code/website/docs/dbr-20261117-01-review.md`）：新增提现申请后，`withdrawableCents` 将由「settled 佣金合计」改为「settled 合计 − (pending+paid 提现申请合计)」，与提现申请同源，避免页面显示可提现额与实际可申请额不一致；`balanceCents` / `earnedCents` 不变。**实测客户端 `60-account.js:490` 只渲染 `balanceCents`，`WithdrawableCents` 仅在 Go struct 解析未渲染 → UI 零影响、无需改代码**，仅同步契约语义 | 账号/后台线 → 客户端线 | 契约升版由后台线执行；客户端线知悉即可，如后续要展示「可提现」请用新口径。**11-18 13:35 客户端复核完毕**：桌面端 `60-account.js` / `index.html` 全仓无 `withdrawableCents` 消费，`internal/account/client.go` 仅 struct 解析；UI 零改动，语义已同步（`go test ./internal/account/` 复跑绿）。备忘：桌面「可提现余额」当前渲染的是 `balanceCents`（含待结算），若日后要求该标签严格等于「可申请提现金额」，改一行取 `withdrawableCents`（需 Go 侧能区分字段缺失，避免零值误显 ¥0） |

## 五、动共享文件前的 10 秒自检

```bash
cd code/dukou
git status --short                      # 谁脏
ls -la --time-style=+%H:%M:%S <目标文件>   # 5 分钟内有改动 = 对方正在改，先等或用分区方案
git log --oneline -3                    # 对方刚提交了什么
```
