// Package firewall 按系统上实际生效的防火墙前端放行端口。
//
// 只处理 firewalld 与 ufw 两个有稳定命令行的前端。nftables 的规则集
// 因发行版而异，自动插入有把用户网络搞坏的风险，因此只给出提示。
package firewall

import (
	"fmt"
	"strings"
)

// Rule 是一条待放行的端口规则。
type Rule struct {
	Port  int
	Proto string // tcp / udp
}

func (r Rule) String() string {
	return fmt.Sprintf("%d/%s", r.Port, r.Proto)
}

// backend 描述一个防火墙前端的命令行形状。
type backend struct {
	// allow 是放行命令，{rule} 会被替换成 "8443/tcp"
	allow [][]string
	// deny 是撤销命令
	deny [][]string
	// needsReload 表示改完需要 reload 才生效
	needsReload bool
}

// Plan 返回放行规则所需的命令序列。
// fw 取 detect.Firewall 的字符串值。
// 返回空切片表示无需操作（无防火墙，或前端不支持自动化）。
func Plan(fw string, rule Rule) [][]string {
	b, ok := backends()[fw]
	if !ok {
		return nil
	}

	var out [][]string
	for _, raw := range b.allow {
		out = append(out, fill(raw, rule))
	}
	if b.needsReload {
		out = append(out, []string{"firewall-cmd", "--reload"})
	}
	return out
}

// UndoPlan 返回撤销放行所需的命令序列。
func UndoPlan(fw string, rule Rule) [][]string {
	b, ok := backends()[fw]
	if !ok {
		return nil
	}

	var out [][]string
	for _, raw := range b.deny {
		out = append(out, fill(raw, rule))
	}
	if b.needsReload {
		out = append(out, []string{"firewall-cmd", "--reload"})
	}
	return out
}

func backends() map[string]backend {
	return map[string]backend{
		"firewalld": {
			allow: [][]string{
				{"firewall-cmd", "--permanent", "--add-port={rule}"},
			},
			deny: [][]string{
				{"firewall-cmd", "--permanent", "--remove-port={rule}"},
			},
			needsReload: true,
		},
		"ufw": {
			allow: [][]string{
				{"ufw", "allow", "{rule}"},
			},
			deny: [][]string{
				{"ufw", "delete", "allow", "{rule}"},
			},
		},
	}
}

func fill(raw []string, rule Rule) []string {
	out := make([]string, len(raw))
	for i, s := range raw {
		out[i] = strings.ReplaceAll(s, "{rule}", rule.String())
	}
	return out
}

// ManualHint 对无法自动处理的前端给出人工操作指引。
// 返回空串表示无需提示。
func ManualHint(fw string, rule Rule) string {
	switch fw {
	case "nftables":
		return fmt.Sprintf("检测到 nftables。为避免破坏现有规则集，dukou 不会自动修改。"+
			"请手工放行 %s，或告诉 dukou 用 --skip-firewall 跳过此步", rule)
	case "none":
		return ""
	}
	return ""
}

// CloudHint 返回云厂商安全组的提示。
//
// 这一层工具无法触及：阿里云/腾讯云/华为云的入方向规则在控制台，
// 没有 API 凭据就改不了。但它是「端口没通」最常见的原因，
// 所以每次部署都必须显式提醒，而不是等用户自己撞上去。
func CloudHint(port int) []string {
	return []string{
		fmt.Sprintf("云控制台安全组（工具无法代为操作）：入方向放行 TCP %d", port),
		"  阿里云：ECS/轻量 → 防火墙 或 安全组 → 入方向 → 添加规则",
		"  漏了这一步的症状是「连接超时」而不是「拒绝」，很难自己看出来",
	}
}

// RenderCommands 把命令序列渲染成可直接粘贴的 shell 文本，用于 --dry-run。
func RenderCommands(cmds [][]string) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, strings.Join(c, " "))
	}
	return out
}
