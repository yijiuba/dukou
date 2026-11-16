package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// UpdateInfo 是更新检查结果（§5.1 契约）。
type UpdateInfo struct {
	Available   bool   `json:"available"`
	Current     string `json:"current"`
	Latest      string `json:"latest"`
	Notes       string `json:"notes"`
	DownloadURL string `json:"downloadUrl"`
	PublishedAt string `json:"publishedAt"`
	Prerelease  bool   `json:"prerelease"`
}

// releaseAPI 指向"最新正式版"。抽成变量是为了测试能指向 httptest 服务。
var releaseAPI = "https://api.github.com/repos/yijiuba/dukou/releases/latest"

// releasePage 用户手动去看更新时打开。
const releasePage = "https://github.com/yijiuba/dukou/releases"

// CheckUpdate 读 GitHub Releases 的最新正式版，与本地版本比较（§八 M3 验收）。
//
// 只在用户点「检查更新」或启动后延迟触发时调用，不做后台轮询——
// 这类工具没必要为了一个版本号常驻一个定时器。
func (s *Service) CheckUpdate() (UpdateInfo, error) {
	log.Printf("[update] check start (current=%s)", Version)
	info, err := fetchLatestRelease(releaseAPI)
	if err != nil {
		log.Printf("[update] check failed: %v", err)
		return UpdateInfo{}, err
	}
	info.Current = Version
	info.Available = compareVersion(info.Latest, Version) > 0
	if !info.Available {
		info.Notes = ""
		info.DownloadURL = releasePage
	}
	log.Printf("[update] check done (latest=%s available=%v)", info.Latest, info.Available)
	return info, nil
}

// fetchLatestRelease 请求 GitHub API。错误一律翻译成用户能看懂的中文。
func fetchLatestRelease(url string) (UpdateInfo, error) {
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return UpdateInfo{}, fmt.Errorf("构造更新请求失败：%v", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "dukou-desktop/"+Version)

	resp, err := client.Do(req)
	if err != nil {
		return UpdateInfo{}, fmt.Errorf("连不上 GitHub 检查更新（网络可能受限）：可稍后重试，或手动访问 %s", releasePage)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return UpdateInfo{}, errors.New("GitHub 限制了本次检查（请求过于频繁），请稍后再试")
	case resp.StatusCode == http.StatusNotFound:
		return UpdateInfo{}, errors.New("仓库还没有发布过正式版本")
	case resp.StatusCode != http.StatusOK:
		return UpdateInfo{}, fmt.Errorf("检查更新失败：GitHub 返回 %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return UpdateInfo{}, fmt.Errorf("读取更新信息失败：%v", err)
	}
	var rel struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		Body        string `json:"body"`
		HTMLURL     string `json:"html_url"`
		Prerelease  bool   `json:"prerelease"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return UpdateInfo{}, errors.New("更新信息格式无法识别，请稍后重试")
	}
	if rel.TagName == "" {
		return UpdateInfo{}, errors.New("更新信息缺少版本号")
	}

	notes := strings.TrimSpace(rel.Body)
	if notes == "" {
		notes = strings.TrimSpace(rel.Name)
	}
	if len([]rune(notes)) > 600 {
		notes = string([]rune(notes)[:600]) + "…"
	}
	dl := rel.HTMLURL
	if dl == "" {
		dl = releasePage
	}
	return UpdateInfo{
		Latest:      strings.TrimPrefix(rel.TagName, "v"),
		Notes:       notes,
		DownloadURL: dl,
		PublishedAt: rel.PublishedAt,
		Prerelease:  rel.Prerelease,
	}, nil
}

// compareVersion 比较两个语义化版本，返回 -1 / 0 / 1。
//
// 只实现更新检查需要的部分：主次修订号 + 预发布标识。
// 规则按 semver：1.2.0 > 1.2.0-rc.1 > 1.2.0-beta；构建元数据（+xxx）不参与比较。
// 容忍缺省段（"0.2" == "0.2.0"）与 v 前缀。
func compareVersion(a, b string) int {
	an, apre := splitVersion(a)
	bn, bpre := splitVersion(b)

	for i := 0; i < 3; i++ {
		if an[i] != bn[i] {
			if an[i] < bn[i] {
				return -1
			}
			return 1
		}
	}
	return comparePrerelease(apre, bpre)
}

// splitVersion 归一化为三段数字 + 预发布串。
func splitVersion(v string) ([3]int, string) {
	var nums [3]int
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	v = strings.TrimPrefix(v, "V")
	if i := strings.IndexByte(v, '+'); i >= 0 { // 构建元数据不参与比较
		v = v[:i]
	}
	pre := ""
	if i := strings.IndexByte(v, '-'); i >= 0 {
		pre = v[i+1:]
		v = v[:i]
	}
	for i, part := range strings.Split(v, ".") {
		if i > 2 {
			break
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nums, pre
		}
		nums[i] = n
	}
	return nums, pre
}

// comparePrerelease：没有预发布标识的版本更大。
func comparePrerelease(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	as := strings.Split(a, ".")
	bs := strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		switch {
		case aerr == nil && berr == nil:
			if an < bn {
				return -1
			}
			return 1
		case aerr == nil: // 数字段优先级低于字符串段
			return -1
		case berr == nil:
			return 1
		default:
			if as[i] < bs[i] {
				return -1
			}
			return 1
		}
	}
	if len(as) < len(bs) {
		return -1
	}
	if len(as) > len(bs) {
		return 1
	}
	return 0
}
