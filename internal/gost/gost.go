// Package gost 负责 gost 二进制的获取与安装。
//
// 三条安装通道，按发行版家族自动选择：
//   - deb  → dpkg -i
//   - rpm  → dnf install / rpm -Uvh
//   - tar.gz → 解压到 /usr/local/bin（任何 Linux 都能走这条兜底）
//
// 之所以三条都要，是因为 gost 是 Go 静态编译的单一二进制，
// rpm/deb 只是便捷通道。遇到陌生发行版时 tar.gz 通道保证永远有路可走。
package gost

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultVersion 是默认安装的 gost 版本。
// 选 3.3.3 是因为 http+tls:// 监听器与 tls:// 转发器在这一版行为稳定，
// 且 TLS 正是绕开明文 CONNECT 域名识别的关键。
const DefaultVersion = "3.3.3"

// ReleaseBase 是 GitHub Release 的下载根地址。
// 单独抽出来是为了将来能指向镜像站。
const ReleaseBase = "https://github.com/go-gost/gost/releases/download"

// Channel 描述一条安装通道。
type Channel struct {
	// Kind 是通道名：deb / rpm / tarball
	Kind string
	// Ext 是资产文件扩展名
	Ext string
	// InstallDir 是二进制最终所在目录
	InstallDir string
}

func (c Channel) BinPath() string {
	// 用 path 而非 filepath：目标永远是 Linux，
	// 在 Windows 上开发时也必须生成正斜杠路径。
	return path.Join(c.InstallDir, "gost")
}

// ChannelFor 按发行版家族选择安装通道。
// family 取 detect.OSFamily 的字符串值（"debian" / "rhel" / 其它）。
func ChannelFor(family string) Channel {
	switch family {
	case "debian":
		return Channel{Kind: "deb", Ext: ".deb", InstallDir: "/usr/bin"}
	case "rhel":
		return Channel{Kind: "rpm", Ext: ".rpm", InstallDir: "/usr/bin"}
	}
	// suse / arch / alpine / unknown 一律走静态二进制，
	// 不依赖任何包管理器，最稳。
	return Channel{Kind: "tarball", Ext: ".tar.gz", InstallDir: "/usr/local/bin"}
}

// assetArch 把 Go 的 GOARCH 翻译成 gost 资产名里的架构段。
func assetArch(goarch string) (string, error) {
	switch goarch {
	case "amd64", "arm64", "386", "arm", "mips64le", "riscv64":
		return goarch, nil
	}
	return "", fmt.Errorf("不支持的架构 %q，请到 %s 查看可用资产", goarch, ReleaseBase)
}

// AssetName 返回资产文件名，例如 gost_3.3.3_linux_amd64.tar.gz。
func AssetName(version, goarch, ext string) (string, error) {
	arch, err := assetArch(goarch)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("gost_%s_linux_%s%s", version, arch, ext), nil
}

// DownloadURL 返回资产的完整下载地址。
func DownloadURL(version, goarch, ext string) (string, error) {
	name, err := AssetName(version, goarch, ext)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/v%s/%s", ReleaseBase, version, name), nil
}

// DetectInstalledVersion 读取已安装 gost 的版本号。
// 返回空串表示未安装。
func DetectInstalledVersion(binPath string) string {
	out, err := exec.Command(binPath, "-V").Output()
	if err != nil {
		return ""
	}
	// 输出形如：gost v3.3.3 (go1.21.5 linux/amd64)
	for _, f := range strings.Fields(string(out)) {
		if strings.HasPrefix(f, "v") && len(f) > 1 && f[1] >= '0' && f[1] <= '9' {
			return strings.TrimPrefix(f, "v")
		}
	}
	return ""
}

// Fetch 下载资产到 destDir，返回本地文件路径与字节数。
// 代理取自环境变量 HTTP_PROXY / HTTPS_PROXY，与 curl 行为一致。
func Fetch(rawURL, destDir string) (path string, size int64, err error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", 0, fmt.Errorf("创建下载目录失败: %w", err)
	}

	client := &http.Client{
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
		},
	}

	resp, err := client.Get(rawURL)
	if err != nil {
		return "", 0, fmt.Errorf("下载 %s 失败: %w\n"+
			"      若本机无法直连 GitHub，可在有网络的机器上下载后拷入，\n"+
			"      或设置 HTTPS_PROXY 后重试", rawURL, unwrapURLError(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("下载 %s 失败: HTTP %d", rawURL, resp.StatusCode)
	}

	name := filepath.Base(rawURL)
	if i := strings.Index(name, "?"); i >= 0 {
		name = name[:i]
	}
	path = filepath.Join(destDir, name)

	f, err := os.Create(path)
	if err != nil {
		return "", 0, err
	}

	size, copyErr := io.Copy(f, resp.Body)
	if copyErr != nil {
		f.Close()
		os.Remove(path)
		return "", 0, fmt.Errorf("写入 %s 失败: %w", path, copyErr)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return "", 0, err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", 0, err
	}

	if err := verifyAsset(path); err != nil {
		os.Remove(path)
		return "", 0, err
	}
	return path, size, nil
}

// verifyAsset 校验下载内容的文件头。
//
// 这一步不是多余的：GitHub 在限流或代理拦截时返回的是 HTML 错误页，
// HTTP 状态码可能仍是 200，文件也「下载成功」了，装的时候才报格式错误。
// 提前检查魔数能把这类失败拦在下载阶段，并给出可理解的错误。
func verifyAsset(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	head := make([]byte, 8)
	n, err := f.Read(head)
	if err != nil && err != io.EOF {
		return err
	}
	head = head[:n]

	st, err := f.Stat()
	if err != nil {
		return err
	}

	switch {
	case strings.HasSuffix(path, ".deb"):
		if !strings.HasPrefix(string(head), "!<arch>") {
			return fmt.Errorf("下载到的不是 deb 包（文件头 %q），可能是代理返回的错误页面", string(head))
		}
	case strings.HasSuffix(path, ".rpm"):
		if n < 4 || head[0] != 0xed || head[1] != 0xab || head[2] != 0xee || head[3] != 0xdb {
			return fmt.Errorf("下载到的不是 rpm 包（文件头 %x），可能是代理返回的错误页面", head)
		}
	case strings.HasSuffix(path, ".tar.gz"), strings.HasSuffix(path, ".tgz"):
		if n < 2 || head[0] != 0x1f || head[1] != 0x8b {
			return fmt.Errorf("下载到的不是 gzip 文件（文件头 %x），可能是代理返回的错误页面", head)
		}
	}

	if st.Size() < 1<<20 {
		return fmt.Errorf("下载文件只有 %d 字节，明显不完整", st.Size())
	}
	return nil
}

// SHA256 返回文件的十六进制摘要，用于安装后记录，便于日后核对。
func SHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// unwrapURLError 把 url.Error 展开成更易懂的文字。
// Go 默认会把完整 URL 塞进错误里，对用户没有帮助。
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// HostArch 返回当前运行架构，测试里可覆盖。
var HostArch = runtime.GOARCH

// ExtractTarball 从 tar.gz 中取出 gost 二进制，放到 destDir 并赋予可执行权限。
// 返回最终路径。这条通道让任何 Linux（含 Alpine / Arch / 未知发行版）都有路可走。
func ExtractTarball(archive, destDir string) (string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("%s 不是有效的 gzip: %w", archive, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// 归档内的二进制可能叫 gost，也可能在子目录里，按 basename 匹配
		if filepath.Base(hdr.Name) != "gost" {
			continue
		}

		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return "", err
		}
		dest := filepath.Join(destDir, "gost")

		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			os.Remove(dest)
			return "", err
		}
		if err := out.Close(); err != nil {
			return "", err
		}
		return dest, nil
	}
	return "", fmt.Errorf("%s 中找不到 gost 可执行文件", archive)
}
