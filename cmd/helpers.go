package cmd

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yijiuba/dukou/internal/gost"
)

// portHolder 尝试找出占用端口的进程名，找不到返回空串。
//
// 这一步的价值在本次排障中体现得很直接：1080 端口看起来「空的」，
// 实际被一个残留的 xray 占着，gost 启动就报 address already in use。
// 提前把占用者名字打印出来，用户就不用自己去猜。
func portHolder(port int) string {
	if !shellHas("ss") {
		return ""
	}
	out, err := exec.Command("ss", "-lntp").Output()
	if err != nil {
		return ""
	}

	needle := ":" + strconv.Itoa(port)
	re := regexp.MustCompile(`\(\("([^"]+)"`)
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, needle) {
			continue
		}
		if m := re.FindStringSubmatch(line); len(m) > 1 {
			return m[1]
		}
	}
	return ""
}

// renderCmds 把命令序列渲染成可粘贴的文本。
func renderCmds(cmds [][]string) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

// probeLocalTLS 请求本机的 TLS 代理端口，返回 HTTP 状态码。
//
// 期望 407：说明 TCP 通、TLS 握手成功、gost 的代理层在正常应答，
// 只是这次请求没带凭据。这三个条件同时成立才算服务端真正可用。
// 这一步不能省——进程 active 不等于端口可用，端口可用也不等于 TLS 正常。
func probeLocalTLS(port int) (string, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			// 探测本机端口不能走系统代理环境变量
			Proxy: nil,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(fmt.Sprintf("https://127.0.0.1:%d", port))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return strconv.Itoa(resp.StatusCode), nil
}

// tcpReachable 判断目标端口能否建立 TCP 连接，用于客户端侧诊断。
func tcpReachable(host string, port int, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

// publicAddress 尽力探测本机公网地址，失败时返回占位提示。
// 不走系统代理：这里要的是这台机器自己的出口地址。
func publicAddress() string {
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{Proxy: nil},
	}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return "<本机公网IP>"
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "<本机公网IP>"
	}
	ip := strings.TrimSpace(string(body))
	if ip == "" {
		return "<本机公网IP>"
	}
	return ip
}

func shellHas(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func lookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func extractTarball(archive, destDir string) (string, error) {
	return gost.ExtractTarball(archive, destDir)
}
