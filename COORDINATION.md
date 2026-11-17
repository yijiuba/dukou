# 并行会话协作公告栏

> 这个仓库同时有 **两个开发会话**在改代码（账号线 T9 ／ 客户端 M3 线）。
> 本文件是两边**唯一的同步点**。规则全文见 `../../资料/开发/并行会话协作约定.md`。
>
> **用法**：动共享文件前，在 §一 登记一行；提交后删掉自己那行。看板超过 30 分钟没更新视为失效，可覆盖。

## 一、当前占用（Claim）

| 登记时间 | 会话 / 任务 | 占用文件 | 意图 | 状态 |
|---|---|---|---|---|
| 11-17 11:52 | 客户端/审计线 C1-C3 | `internal/app/connect.go`（SwitchServer/Connect 加锁检查）、`internal/app/{lockcheck.go,lockcheck_test.go}`（新增）、`internal/store/lockstate.go`（新增）、`internal/app/app.go`（struct 字段区末尾追加）、`desktop/app.go`（末尾追加转发）、`index.html`（服务器页分区+st3+806-813 清单+expiredModal 文案）、`js/20-api.js`（DukouAPI 末尾追加 subscriptionLock） | 对接单 v1.1 D2 定稿落地：expiredDemo→真实 locked、SwitchServer 加 SUB:LOCKED、文案修正 | 进行中 |
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

## 五、动共享文件前的 10 秒自检

```bash
cd code/dukou
git status --short                      # 谁脏
ls -la --time-style=+%H:%M:%S <目标文件>   # 5 分钟内有改动 = 对方正在改，先等或用分区方案
git log --oneline -3                    # 对方刚提交了什么
```
