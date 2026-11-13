# 构建、测试与发版

> 给改代码的人。环境搭一次，之后只有三条常用命令。

## 环境要求

| 依赖 | 版本 | 用途 |
|---|---|---|
| Go | ≥ 1.22（以 `go.mod` 为准） | 全部 |
| GTK/WebKit 开发库 | `libgtk-3-dev libwebkit2gtk-4.1-dev` | 仅 Linux 上编译桌面版 |
| WebView2 | Windows 10+ 自带 | 仅 Windows 桌面版运行时 |

## 常用命令

```bash
go build -o dukou .        # 编译命令行版
go test ./...              # 跑全部单元测试
go vet ./...               # 静态检查（CI 会跑，提交前先跑）
```

Linux 上建议加竞态检测（CI 也是这么做的）：

```bash
go test ./... -race -count=1
```

## 桌面版

桌面版带 `gui` 构建标签，与 CLI 共用 `internal/` 内核：

```bash
go build -tags gui -trimpath -ldflags "-s -w" -o dukou-desktop ./desktop
```

**桌面 exe 不支持 CLI 子命令**，它是纯窗口程序，内部调 `deploy` 同一条流水线。

## 交叉编译（发布物）

发布物必须是纯静态的（目标机器可能是 musl 或没有 libc 开发包）：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -trimpath -ldflags "-s -w -X github.com/yijiuba/dukou/cmd.Version=0.1.0" \
  -o dist/dukou-linux-amd64 .
```

- 版本号通过 `-X ...cmd.Version=` 注入，不注入时显示 `0.1.0-dev`
- 发布矩阵：`linux/amd64`、`linux/arm64`、`linux/386`

## CI（.github/workflows/ci.yml）

每次 push 到 main 或 PR 触发，14 个检查项，全绿才算完成：

| Job | 内容 |
|---|---|
| unit ×3 | ubuntu/windows/macos 上 `go vet` + `go test` |
| build ×3 | 三架构静态交叉编译，产物上传为 artifact |
| smoke ×5 | 把二进制丢进 5 种发行版容器，验证 `version` / `doctor` 家族识别 / `server --dry-run` 无副作用 |
| desktop ×2 | ubuntu + windows 编译桌面版 |
| ci-ok | 汇总门禁 |

### CI 的四个已知坑（都已在 workflow 注释里写明原因）

1. **`-race` 与 `CGO_ENABLED=0` 冲突** → 竞态检测只在 Linux job 跑，CGO 限制只加在 build job
2. **macOS arm64 的 `-race` 二进制缺 LC_UUID** → macOS 不跑竞态
3. **macOS 上 cgo 解析器同样缺 LC_UUID** → macOS 测试加 `-tags netgo`
4. **Windows runner 默认 PowerShell 不认 `\` 续行** → 相关 step 显式 `shell: bash`

## 发版流程

**打 tag 即发版。** `.github/workflows/release.yml` 监听 `v*` tag：

```bash
git tag v0.1.0 && git push origin v0.1.0
```

自动完成：三架构 CLI（版本号从 tag 注入）+ 两平台桌面版编译、sha256 校验和、创建 GitHub Release 并挂上全部产物。之后：

1. 用户无需登录即可在 Releases 页面下载
2. `dukou version` 显示正式版本号（去掉 `v` 前缀后的 tag 名）
3. 发版前把 [CHANGELOG](../../CHANGELOG.md) 的 Unreleased 段整理成对应版本号并写上日期

## 本机环境备忘（Windows + Git Bash）

- **MSYS 路径转换**会把 `/root/dukou` 这类参数改写成 Windows 路径。涉及绝对路径参数时先 `export MSYS_NO_PATHCONV=1`，本地文件一律用相对路径
- 本机 shell 可能带着 `HTTP_PROXY` 等代理变量，curl GitHub 失败时先 `env -u HTTP_PROXY -u HTTPS_PROXY` 再试
- 解压 Go 的 zip 发行包用 `tar.exe -xf`，不要用资源管理器或 PowerShell 的 Expand-Archive（可能解不完整）
