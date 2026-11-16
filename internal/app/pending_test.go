package app

import (
	"strings"
	"testing"

	"github.com/yijiuba/dukou/internal/store"
)

// 「先保存后部署」流程的守卫测试：
//
// 1) 保存时传 status=pending，清单里就是待部署；编辑不改状态（状态只由
//    部署成功 SetStatus("standby") 或连接失败置 error 来改）。
// 2) 待部署的服务器不允许连接/切换——服务端还没装，连了只会报出
//    更让人糊涂的错。且切换必须在断开当前线路**之前**被拦下，
//    不能让用户从「连着」变成「断了还没连上」。
func TestAddServerPendingStatus(t *testing.T) {
	s, _, fp := switchFixture(t)

	pend, err := s.store.Add(store.ServerInput{
		Name:        "东京 · 待部署",
		Host:        "10.0.0.9",
		AuthType:    "password",
		SSHPassword: "pw",
		ProxyPort:   7890,
		ServerPort:  8443,
		Status:      "pending",
	})
	if err != nil {
		t.Fatalf("Add(pending): %v", err)
	}
	if pend.Status != "pending" {
		t.Fatalf("保存时状态应保持 pending，实际 %q", pend.Status)
	}

	// 不传 Status 的老入口（网络配置页即填即部署）默认仍是 standby
	std, err := s.store.Add(store.ServerInput{
		Name: "新加坡 · 已部署", Host: "10.0.0.8", AuthType: "password",
		SSHPassword: "pw", ProxyPassword: "pp", ProxyPort: 7890, ServerPort: 8443,
	})
	if err != nil {
		t.Fatalf("Add(standby): %v", err)
	}
	if std.Status != "standby" {
		t.Fatalf("老入口默认状态应为 standby，实际 %q", std.Status)
	}

	// 编辑（密码留空 = 保持原凭据）不能把状态改掉
	up, err := s.store.Update(pend.ID, store.ServerInput{
		Name: pend.Name, Host: "10.0.0.10", AuthType: "password",
		SSHUser: "root", SSHPort: 22, ProxyPort: 7890, ServerPort: 8443,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if up.Status != "pending" {
		t.Fatalf("编辑后状态应保持 pending，实际 %q", up.Status)
	}
	if up.Host != "10.0.0.10" {
		t.Fatalf("编辑后的 Host 应生效，实际 %q", up.Host)
	}

	// 待部署：连接被拦，且不碰代理
	_, err = s.Connect(pend.ID)
	if err == nil || !strings.Contains(err.Error(), "部署") {
		t.Fatalf("连接待部署服务器应报「部署」相关错误，实际 %v", err)
	}
	// 切换也拦，且必须发生在断开之前——当前线路不该被动
	if err := s.SwitchServer(pend.ID); err == nil || !strings.Contains(err.Error(), "部署") {
		t.Fatalf("切换到待部署服务器应报「部署」相关错误，实际 %v", err)
	}
	if len(fp.calls) != 0 {
		t.Fatalf("全程不该碰代理，却记录到 %v", fp.calls)
	}
}
