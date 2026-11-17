package proxy

import (
	"net"
	"strconv"
	"time"
)

// TCPRTTMS 直拨 host:port 量 TCP 建连 RTT，连 3 次取最小值。
//
// 为什么不用 HTTPS 请求耗时当延迟（2026-11-17 真机反馈）：走隧道完整请求
// 一次 api.ipify.org 包含 TLS 全握手 + ipify 自身的响应时间，新加坡线路
// 实测能堆到 5 秒多——用户会误以为线路卡爆，其实 ping 只有几十毫秒。
// TCP 建连是不带任何应用层开销的纯网络往返，才是「延迟」的本意。
//
// 3 次取最小：单次建连可能撞上瞬时抖动，最小值最接近真实链路时延。
// 全部失败返回 0（前端显示 —，与「连不通」的语义一致，不编造数字）。
func TCPRTTMS(host string, port int) int {
	if host == "" || port <= 0 {
		return 0
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	best := 0
	for i := 0; i < 3; i++ {
		start := time.Now()
		c, err := net.DialTimeout("tcp", addr, 1500*time.Millisecond)
		if err != nil {
			continue
		}
		_ = c.Close()
		// 微秒精度取整：本机/内网 RTT 亚毫秒，Millisecond() 直接截断会得 0，
		// 与「连不通返回 0」混淆。四舍五入且至少 1ms。
		us := time.Since(start).Microseconds()
		ms := int((us + 500) / 1000)
		if ms < 1 {
			ms = 1
		}
		if best == 0 || ms < best {
			best = ms
		}
	}
	return best
}
