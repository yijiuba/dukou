// Package proxy 在本机起一个转发进程，并把系统代理指向它（§四 方案 B）。
//
// 与 internal/deploy 的分工：
//   - deploy 负责「服务器上有什么」，产出可用的上游凭据；
//   - proxy 负责「本机怎么用」，起进程、改系统代理、断开时还原。
//
// 为什么单独成包：连接/断开是常驻能力（托盘里随时切），
// 而部署是一次性动作。混在一起会让「断开」也不得不走一遍部署流程。
package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

// Status 是界面读的代理状态（§5.1 契约）。
type Status struct {
	Connected  bool   `json:"connected"`
	ServerID   string `json:"serverId"`
	ServerName string `json:"serverName"`
	LocalPort  int    `json:"localPort"`
	EgressIP   string `json:"egressIp"`
	LatencyMS  int    `json:"latencyMs"`
	Since      string `json:"since"`
	// Residue 为 true 表示上次异常退出留下了系统代理设置，需要一键清理
	Residue bool `json:"residue"`
}

// Upstream 是上游出口服务器。
type Upstream struct {
	ID       string
	Name     string
	Host     string
	Port     int
	User     string
	Password string
}

// Fetcher 把远端文件下载到本地路径。
//
// 为什么不让本包自己走 HTTP：本机此刻可能根本出不了国，
// 唯一能用的出口就是刚装好的那台服务器。由调用方注入
// 「经服务器中转」的实现，本包就不必关心网络路径。
type Fetcher func(ctx context.Context, rawURL, dest string) error

// Options 是本机侧的运行参数。
type Options struct {
	// DataDir 存放 gost 二进制与状态快照，通常是 %AppData%\dukou
	DataDir   string
	HTTPPort  int
	SOCKSPort int
}

// 默认本地监听端口。选 7890/7891 是 Clash 系的通行约定，
// 用户已有的工具链（Codex、各类 IDE 插件）默认就认这两个。
const (
	DefaultHTTPPort  = 7890
	DefaultSOCKSPort = 7891
)

// Normalize 补齐默认值。
func (o Options) Normalize() Options {
	if o.HTTPPort == 0 {
		o.HTTPPort = DefaultHTTPPort
	}
	if o.SOCKSPort == 0 {
		o.SOCKSPort = DefaultSOCKSPort
	}
	return o
}

// binName 是本机转发进程的文件名。
// Windows 上必须带 .exe，否则 exec 找不到；其余平台不带。
var binName = func() string {
	if runtime.GOOS == "windows" {
		return "gost.exe"
	}
	return "gost"
}()

// BinPath 返回随包/下载的 gost 可执行文件路径。
func (o Options) BinPath() string {
	return filepath.Join(o.DataDir, "bin", binName)
}

// statePath 是状态快照文件。
//
// 这个文件的用途只有一个：让「还原」在崩溃、断电、强杀之后依然可行。
// 只存在内存里的话，进程一死就再也想不起连接前的代理是什么样了。
func (o Options) statePath() string {
	return filepath.Join(o.DataDir, "proxy-state.json")
}

// state 是落盘的状态快照。
type state struct {
	Since      string `json:"since"`
	ServerID   string `json:"serverId"`
	ServerName string `json:"serverName"`
	LocalPort  int    `json:"localPort"`
	// PID 用于断开时找到上次起的进程；进程已死则忽略
	PID int `json:"pid"`

	// ── 连接前系统代理的原始值，还原时照抄 ──
	ProxyEnable   int    `json:"proxyEnable"`
	ProxyServer   string `json:"proxyServer"`
	ProxyOverride string `json:"proxyOverride"`
	// AutoConfigURL 是 PAC 地址。它存在时会压过 ProxyServer，
	// 设置手工代理前必须先摘掉，因此还原时必须原样写回。
	AutoConfigURL string `json:"autoConfigUrl"`

	// Had* 记录这几项原本是否存在。
	// 区分「原本是 0」和「原本没有这个键」很重要：
	// 前者写回 0，后者必须删除，否则会在注册表里留下垃圾。
	HadEnable   bool `json:"hadEnable"`
	HadServer   bool `json:"hadServer"`
	HadOverride bool `json:"hadOverride"`
	HadAutoConf bool `json:"hadAutoConfig"`
}

// readState 读取状态快照。文件不存在返回 (nil, nil)。
func readState(o Options) (*state, error) {
	b, err := os.ReadFile(o.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s state
	if err := json.Unmarshal(b, &s); err != nil {
		// 快照损坏时不能当成「没有快照」——那会直接放弃还原。
		// 保留文件让用户能人工看一眼，同时把错误抛上去。
		return nil, fmt.Errorf("状态快照已损坏（%s）：%w", o.statePath(), err)
	}
	return &s, nil
}

// writeState 原子写入快照。
//
// 先写临时文件再 rename：这个文件是还原的唯一依据，
// 写到一半断电会得到一份既不能读、又覆盖了旧内容的残骸。
func writeState(o Options, s *state) error {
	if err := os.MkdirAll(o.DataDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := o.statePath() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, o.statePath())
}

// clearState 删除快照。还原成功后调用。
func clearState(o Options) error {
	err := os.Remove(o.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// portFree 判断本机端口能否监听。
//
// 用实际 listen 而不是「查占用表」：Windows 上 TIME_WAIT、保留端口、
// Hyper-V 的动态端口保留区都会让结论和 listen 结果不一致。
// 唯一可靠的判断就是真去 bind 一次。
func portFree(port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// pickPorts 挑一对可用的本地端口。
//
// 端口被占时报「连接成功」是最坏的结果：系统代理指向 127.0.0.1:7890，
// 那里却应答着别人的代理，用户以为连上了海外服务器，
// 实际流量走了另一个出口——v0.1 踩过这个坑。
// 所以这里不假设默认端口可用，逐个往后试。
func pickPorts(httpPort, socksPort int) (int, int, error) {
	pick := func(start int) (int, error) {
		for i := 0; i < 20; i++ {
			p := start + i
			if p > 65535 {
				break
			}
			if portFree(p) {
				return p, nil
			}
		}
		return 0, fmt.Errorf("从 %d 起连续 20 个端口都被占用，请先关掉占用程序再试", start)
	}

	hp, err := pick(httpPort)
	if err != nil {
		return 0, 0, err
	}
	// SOCKS 端口不能和 HTTP 撞在同一个上，否则第二个监听必然失败
	sp, err := pick(socksPort)
	if err != nil {
		return 0, 0, err
	}
	for sp == hp {
		sp, err = pick(sp + 1)
		if err != nil {
			return 0, 0, err
		}
	}
	return hp, sp, nil
}

// proxyBypass 是写进系统代理「不使用代理」列表的内容。
//
// 少了国内域名后缀，访问国内站点会绕一圈出国再回来，
// 慢且容易被风控；少了私有网段，局域网设备会直接连不上。
const proxyBypass = "localhost;127.*;10.*;172.16.*;172.17.*;172.18.*;172.19.*;" +
	"172.2*;172.30.*;172.31.*;192.168.*;<local>;" +
	"*.cn;*.aliyun.com;*.aliyuncs.com;*.taobao.com;*.tmall.com;*.qq.com;*.tencent.com"

// sinceNow 返回当前时间的 RFC3339 字符串。
func sinceNow() string { return time.Now().Format(time.RFC3339) }
