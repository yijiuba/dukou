package app

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/yijiuba/dukou/internal/proxy"
	"github.com/yijiuba/dukou/internal/store"
)

// fakeProxy 记录调用顺序，并能按需制造失败。
//
// 之所以能这么测：切换线路的价值全在「顺序」和「失败后状态」上，
// 真实 proxy 会起进程、改注册表，测试机上既跑不了也不该跑。
type fakeProxy struct {
	current  proxy.Status
	startErr error
	stopErr  error
	calls    []string
}

func (f *fakeProxy) Start(up proxy.Upstream, _ proxy.Fetcher) (proxy.Status, error) {
	f.calls = append(f.calls, "start:"+up.ID)
	if f.startErr != nil {
		return proxy.Status{}, f.startErr
	}
	f.current = proxy.Status{Connected: true, ServerID: up.ID, ServerName: up.Name}
	return f.current, nil
}

func (f *fakeProxy) Stop() error {
	f.calls = append(f.calls, "stop")
	if f.stopErr != nil {
		return f.stopErr
	}
	f.current = proxy.Status{}
	return nil
}

func (f *fakeProxy) Current() (proxy.Status, error) { return f.current, nil }

// switchFixture 建一台带 3 台服务器、注入了 fake 代理的服务。
func switchFixture(t *testing.T) (*Service, []store.Server, *fakeProxy) {
	t.Helper()
	s := newTestService(t)
	s.Startup(context.Background())
	fp := &fakeProxy{}
	s.pdrv = fp

	names := []string{"东京 · Vultr", "新加坡 · 阿里云", "洛杉矶 · RackNerd"}
	servers := make([]store.Server, 0, len(names))
	for i, name := range names {
		srv, err := s.store.Add(store.ServerInput{
			Name:        name,
			Host:        "10.0.0." + strconv.Itoa(i+1),
			AuthType:    "password",
			SSHPassword: "pw",
			// 代理密码是 Connect 的前置条件（没有它连不上上游），
			// 真实场景由部署结果回填，测试里直接给。
			ProxyPassword: "proxy-pw",
			ProxyPort:     7890,
			ServerPort:    8443,
		})
		if err != nil {
			t.Fatalf("Add(%s): %v", name, err)
		}
		servers = append(servers, srv)
	}
	return s, servers, fp
}

// statusOf 读当前清单里某台服务器的状态标记。
func statusOf(t *testing.T, s *Service, id string) string {
	t.Helper()
	srv, ok := s.store.Get(id)
	if !ok {
		t.Fatalf("服务器 %s 不在清单里", id)
	}
	return srv.Status
}

// 切换必须先断旧、再连新。顺序反了会把旧线路的原始系统代理配置
// 当成新连接的快照，最后还原到一个谁也没配过的状态。
func TestSwitchServerStopsBeforeStarting(t *testing.T) {
	s, servers, fp := switchFixture(t)
	oldSrv, newSrv := servers[0], servers[1]

	if _, err := s.Connect(oldSrv.ID); err != nil {
		t.Fatalf("Connect(%s): %v", oldSrv.Name, err)
	}
	fp.calls = nil // 只看切换这一段

	if err := s.SwitchServer(newSrv.ID); err != nil {
		t.Fatalf("SwitchServer: %v", err)
	}

	want := []string{"stop", "start:" + newSrv.ID}
	if len(fp.calls) != len(want) {
		t.Fatalf("调用序列不对：got %v want %v", fp.calls, want)
	}
	for i := range want {
		if fp.calls[i] != want[i] {
			t.Fatalf("调用序列不对：got %v want %v", fp.calls, want)
		}
	}
	if got := statusOf(t, s, oldSrv.ID); got != "standby" {
		t.Fatalf("旧线路应回到 standby，实际 %q", got)
	}
	if got := statusOf(t, s, newSrv.ID); got != "active" {
		t.Fatalf("新线路应为 active，实际 %q", got)
	}
	if fp.current.ServerID != newSrv.ID {
		t.Fatalf("驱动当前线路应是新线路，实际 %q", fp.current.ServerID)
	}
}

// 断开失败时必须中止：继续连新的会同时留着旧进程和旧系统代理设置，
// 之后再也分不清该还原成什么样。
func TestSwitchServerAbortsWhenStopFails(t *testing.T) {
	s, servers, fp := switchFixture(t)
	if _, err := s.Connect(servers[0].ID); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	fp.calls = nil
	fp.stopErr = errors.New("还原系统代理失败")

	err := s.SwitchServer(servers[1].ID)
	if err == nil {
		t.Fatal("断开失败时切换应报错")
	}
	for _, c := range fp.calls {
		if len(c) > 5 && c[:5] == "start" {
			t.Fatalf("断开失败后不应再启动新线路，调用序列 %v", fp.calls)
		}
	}
	if got := statusOf(t, s, servers[1].ID); got == "active" {
		t.Fatalf("切换失败后新线路不应标记为 active，实际 %q", got)
	}
}

// 目标服务器不存在时，必须在断开之前就返回：
// 否则用户会从「连着」变成「断了且没连上」，而这次点击本来什么也不该改。
func TestSwitchServerKeepsCurrentWhenTargetMissing(t *testing.T) {
	s, servers, fp := switchFixture(t)
	if _, err := s.Connect(servers[0].ID); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	fp.calls = nil

	if err := s.SwitchServer("deleted-server-id"); err == nil {
		t.Fatal("切换不存在的服务器应报错")
	}
	if len(fp.calls) != 0 {
		t.Fatalf("目标不存在时不应碰代理，实际调用 %v", fp.calls)
	}
	if got := statusOf(t, s, servers[0].ID); got != "active" {
		t.Fatalf("原线路应保持 active，实际 %q", got)
	}
	if !fp.current.Connected {
		t.Fatal("原线路应仍然连接着")
	}
}

// 新线路连不上时，新线路标 error、旧线路回 standby，界面不能出现两台使用中。
func TestSwitchServerMarksTargetErrorWhenStartFails(t *testing.T) {
	s, servers, fp := switchFixture(t)
	if _, err := s.Connect(servers[0].ID); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	fp.startErr = errors.New("上游认证失败")

	if err := s.SwitchServer(servers[1].ID); err == nil {
		t.Fatal("新线路连不上时应报错")
	}
	if got := statusOf(t, s, servers[1].ID); got != "error" {
		t.Fatalf("新线路应标 error，实际 %q", got)
	}
	if got := statusOf(t, s, servers[0].ID); got != "standby" {
		t.Fatalf("旧线路应回 standby，实际 %q", got)
	}
}

// 状态快照丢失时（用户删了 proxy-state.json、旧版本遗留），旧服务器会停在
// active 再也回不去。连接成功后统一收口，保证同一时刻至多一台 active。
func TestConnectClearsStaleActiveFlags(t *testing.T) {
	s, servers, _ := switchFixture(t)
	// 手工制造两台残留 active
	for _, id := range []string{servers[0].ID, servers[1].ID} {
		if err := s.store.SetStatus(id, "active"); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}
	}

	if _, err := s.Connect(servers[2].ID); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	for _, srv := range s.store.List() {
		if srv.ID == servers[2].ID {
			if srv.Status != "active" {
				t.Fatalf("目标线路应 active，实际 %q", srv.Status)
			}
			continue
		}
		if srv.Status == "active" {
			t.Fatalf("%s 残留 active 未被清理", srv.Name)
		}
	}
}

// 三台之间来回切换：每次都只能有一台 active，且驱动只认最后一台。
func TestSwitchServerAcrossThreeServers(t *testing.T) {
	s, servers, fp := switchFixture(t)
	if _, err := s.Connect(servers[0].ID); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	for _, target := range []store.Server{servers[1], servers[2], servers[0]} {
		if err := s.SwitchServer(target.ID); err != nil {
			t.Fatalf("SwitchServer(%s): %v", target.Name, err)
		}
		active := 0
		for _, srv := range s.store.List() {
			if srv.Status == "active" {
				active++
				if srv.ID != target.ID {
					t.Fatalf("切到 %s 后 active 的是 %s", target.Name, srv.Name)
				}
			}
		}
		if active != 1 {
			t.Fatalf("切到 %s 后有 %d 台 active，应恰好 1 台", target.Name, active)
		}
		if fp.current.ServerID != target.ID {
			t.Fatalf("驱动当前线路应是 %s，实际 %s", target.Name, fp.current.ServerID)
		}
	}
}
