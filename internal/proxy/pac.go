//go:build windows

package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// PAC 分流（§四「从零实现」）。
//
// 原理：连接时不写固定代理（ProxyEnable=1），而是生成一个 PAC 脚本写到
// 用户数据目录，把系统 AutoConfigURL 指过去。浏览器等 WinINet 客户端
// 对每个请求先问 PAC：国内域名 → DIRECT（直连，不出国），其余 → 本机 gost。
//
// 为什么不用完整 chnroutes（约 5000 条 CIDR）+ 全量 china-domain-list
// （约 7000 域名）：那会让 PAC 文件膨胀到几百 KB，WinINet 每次请求
// 都要跑一遍匹配，明显变慢。v0.2 用「后缀域名清单 + .cn 通配」
// 覆盖高频国内站点，未覆盖的域名走代理——宁可多绕路，不可断网。
// 后续版本可换可更新的在线规则源。

// cnDomains 是国内高频站点后缀清单（.cn 不用列——用通配整体直连）。
// 银行/支付类必须直连：走海外出口访问网银会触发风控。
var cnDomains = []string{
	// 电商 / 支付
	"taobao.com", "tmall.com", "jd.com", "pinduoduo.com", "yangkeduo.com",
	"alipay.com", "tenpay.com",
	// 阿里系
	"alibaba.com", "aliyun.com", "aliyundrive.com", "alipan.com",
	"amap.com", "autonavi.com", "dingtalk.com",
	// 腾讯系
	"qq.com", "tencent.com", "myqcloud.com", "qcloud.com",
	"weixin.com", "wechat.com", "weiyun.com", "gtimg.cn",
	// 内容 / 社交
	"bilibili.com", "hdslb.com", "biliapi.net", "acfun.cn", "zhihu.com",
	"weibo.com", "weibo.cn", "sina.com", "sina.cn", "sohu.com", "itc.cn",
	"douyin.com", "douyinpic.com", "kuaishou.com", "ixigua.com", "toutiao.com",
	"bytedance.com", "snssdk.com", "iqiyi.com", "youku.com", "mgtv.com",
	"xueqiu.com", "eastmoney.com", "10jqka.com.cn", "hexun.com",
	// 搜索 / 门户
	"baidu.com", "bdstatic.com", "bdimg.com", "bcebos.com",
	"163.com", "126.com", "126.net", "netease.com",
	"so.com", "sogou.com", "hao123.com",
	// 生活服务
	"meituan.com", "dianping.com", "ele.me", "ctrip.com", "qunar.com",
	"12306.cn", "didichuxing.com",
	// 手机厂商
	"xiaomi.com", "mi.com", "miui.com", "oppo.com", "vivo.com",
	"huawei.com", "hicloud.com", "vmall.com", "honor.com", "realme.com",
	// 基础设施 / 办公
	"wps.cn", "wps.com", "kingsoft.com", "xunlei.com",
	"10086.cn", "10010.com", "189.cn", "chinaunicom.cn", "chinatelecom.cn",
	// 游戏
	"mihoyo.com", "yuanshen.com", "kurogames.com",
	// 教育
	"xuexi.cn", "chaoxing.com", "icourse163.org",
}

// privateNets 是直连必过的内网段（PAC 里用 isInNet 判断）。
// 不加的话，访问路由器管理页 / NAS / 公司内网会被送进代理，直接断连。
var privateNets = [][2]string{
	{"10.0.0.0", "255.0.0.0"},
	{"172.16.0.0", "255.240.0.0"},
	{"192.168.0.0", "255.255.0.0"},
	{"127.0.0.0", "255.0.0.0"},
	{"169.254.0.0", "255.255.0.0"},
}

// pacPath 返回 PAC 脚本落盘位置。
func pacPath(o Options) string {
	return filepath.Join(o.Normalize().DataDir, "proxy.pac")
}

// pacFileURL 把本地路径转成 AutoConfigURL 接受的 file:// URL。
// Windows 路径反斜杠必须换成正斜杠，且要三个斜杠开头。
func pacFileURL(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

// buildPAC 生成 PAC 脚本。port 是本机 HTTP 代理端口。
//
// 语法刻意停在 ES3：WinINet 的 PAC 引擎是老 JScript，
// 没有箭头函数、没有 endsWith、没有 includes——用了就全军覆没，
// 表现是分流静默失效且无任何报错。
func buildPAC(port int) string {
	var b strings.Builder
	b.WriteString("// 渡口 PAC 分流：国内直连，国外走本机代理。由程序生成，请勿手改。\n")
	b.WriteString("function FindProxyForURL(url, host) {\n")
	b.WriteString("  host = String(host).toLowerCase();\n")
	b.WriteString("  if (isPlainHostName(host)) return 'DIRECT';\n")
	// 字面量 IP（用户直接输 IP 访问）不需要解析域名
	b.WriteString("  var ip = /^\\d+\\.\\d+\\.\\d+\\.\\d+$/.test(host) ? host : dnsResolve(host);\n")
	for _, n := range privateNets {
		fmt.Fprintf(&b, "  if (ip && isInNet(ip, '%s', '%s')) return 'DIRECT';\n", n[0], n[1])
	}
	// .cn 全量直连
	b.WriteString("  if (host == 'cn' || shExpMatch(host, '*.cn')) return 'DIRECT';\n")
	// 后缀清单
	b.WriteString("  if (m(host, [")
	domains := append([]string(nil), cnDomains...)
	sort.Strings(domains)
	for i, d := range domains {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "'%s'", d)
	}
	b.WriteString("])) return 'DIRECT';\n")
	fmt.Fprintf(&b, "  return 'PROXY 127.0.0.1:%d';\n}\n", port)
	b.WriteString(`
function m(h, list) {
  for (var i = 0; i < list.length; i++) {
    var d = list[i];
    if (h == d) return true;
    var n = h.length - d.length - 1;
    if (n > 0 && h.substring(n) == '.' + d) return true;
  }
  return false;
}
`)
	return b.String()
}

// WritePAC 把 PAC 脚本写到用户数据目录，返回脚本路径与 AutoConfigURL 值。
func WritePAC(o Options, port int) (path, autoconfigURL string, err error) {
	o = o.Normalize()
	path = pacPath(o)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(path, []byte(buildPAC(port)), 0o644); err != nil {
		return "", "", fmt.Errorf("写 PAC 脚本失败: %w", err)
	}
	return path, pacFileURL(path), nil
}

// RemovePAC 删除 PAC 脚本（断开还原后清理）。
func RemovePAC(o Options) {
	_ = os.Remove(pacPath(o))
}

// systemPACIsOurs 判断当前 AutoConfigURL 是否指向本工具生成的 PAC 文件。
// 只比对路径，不写文件。
func SystemPACIsOurs(o Options) bool {
	want := pacFileURL(pacPath(o))
	k, err := registry.OpenKey(registry.CURRENT_USER, inetKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue("AutoConfigURL")
	return err == nil && v == want
}
