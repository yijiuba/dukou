package firewall

import (
	"strings"
	"testing"
)

func TestPlanFirewalld(t *testing.T) {
	cmds := Plan("firewalld", Rule{Port: 8443, Proto: "tcp"})
	if len(cmds) != 2 {
		t.Fatalf("firewalld 应为 2 条命令（add-port + reload），实际 %d: %v", len(cmds), cmds)
	}
	first := strings.Join(cmds[0], " ")
	if first != "firewall-cmd --permanent --add-port=8443/tcp" {
		t.Errorf("放行命令错误: %s", first)
	}
	last := strings.Join(cmds[1], " ")
	if last != "firewall-cmd --reload" {
		t.Errorf("最后应为 reload: %s", last)
	}
}

func TestPlanUFW(t *testing.T) {
	cmds := Plan("ufw", Rule{Port: 8443, Proto: "tcp"})
	if len(cmds) != 1 {
		t.Fatalf("ufw 应为 1 条命令，实际 %d: %v", len(cmds), cmds)
	}
	got := strings.Join(cmds[0], " ")
	if got != "ufw allow 8443/tcp" {
		t.Errorf("ufw 命令错误: %s", got)
	}
}

func TestPlanUnsupportedBackendsReturnNil(t *testing.T) {
	for _, fw := range []string{"nftables", "none", "unknown"} {
		if cmds := Plan(fw, Rule{Port: 8443, Proto: "tcp"}); cmds != nil {
			t.Errorf("%s 不应返回命令，实际 %v", fw, cmds)
		}
	}
}

func TestUndoPlanFirewalld(t *testing.T) {
	cmds := UndoPlan("firewalld", Rule{Port: 8443, Proto: "tcp"})
	if len(cmds) != 2 {
		t.Fatalf("应为 2 条，实际 %d", len(cmds))
	}
	if got := strings.Join(cmds[0], " "); got != "firewall-cmd --permanent --remove-port=8443/tcp" {
		t.Errorf("撤销命令错误: %s", got)
	}
}

func TestUndoPlanUFW(t *testing.T) {
	cmds := UndoPlan("ufw", Rule{Port: 8443, Proto: "tcp"})
	if len(cmds) != 1 {
		t.Fatalf("应为 1 条，实际 %d", len(cmds))
	}
	if got := strings.Join(cmds[0], " "); got != "ufw delete allow 8443/tcp" {
		t.Errorf("撤销命令错误: %s", got)
	}
}

func TestRuleString(t *testing.T) {
	r := Rule{Port: 8443, Proto: "tcp"}
	if got := r.String(); got != "8443/tcp" {
		t.Errorf("应为 8443/tcp，实际 %s", got)
	}
}

func TestManualHint(t *testing.T) {
	if h := ManualHint("nftables", Rule{Port: 8443, Proto: "tcp"}); !strings.Contains(h, "8443/tcp") {
		t.Errorf("nftables 提示应含端口: %s", h)
	}
	if h := ManualHint("none", Rule{Port: 8443, Proto: "tcp"}); h != "" {
		t.Errorf("无防火墙时不应提示，实际 %s", h)
	}
	if h := ManualHint("firewalld", Rule{Port: 8443, Proto: "tcp"}); h != "" {
		t.Errorf("firewalld 可自动处理，不应给出人工提示，实际 %s", h)
	}
}

func TestCloudHintMentionsPort(t *testing.T) {
	hint := strings.Join(CloudHint(8443), " ")
	if !strings.Contains(hint, "8443") {
		t.Errorf("安全组提示必须含端口号: %s", hint)
	}
	if !strings.Contains(hint, "阿里云") {
		t.Errorf("应给出阿里云的具体路径: %s", hint)
	}
}

func TestRenderCommands(t *testing.T) {
	cmds := [][]string{
		{"firewall-cmd", "--permanent", "--add-port=8443/tcp"},
		{"firewall-cmd", "--reload"},
	}
	out := RenderCommands(cmds)
	if len(out) != 2 {
		t.Fatalf("应为 2 行，实际 %d", len(out))
	}
	if out[0] != "firewall-cmd --permanent --add-port=8443/tcp" {
		t.Errorf("渲染错误: %s", out[0])
	}
}
