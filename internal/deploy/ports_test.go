package deploy

import (
	"strings"
	"testing"

	"github.com/yijiuba/dukou/internal/remote"
)

// fakePortExec 模拟 ss / cgroup 的应答，用来覆盖端口占用判断。
type fakePortExec struct {
	// listen 是端口上是否有监听
	listen bool
	// pid 是监听者的进程号，0 表示看不到（权限不足）
	pid int
	// unit 是监听者所属的 systemd 单元
	unit string
	// broken 为真时所有命令都报错
	broken bool
}

func (f *fakePortExec) Label() string { return "fake" }
func (f *fakePortExec) Close() error  { return nil }

func (f *fakePortExec) Run(cmd string) (string, error) {
	if f.broken {
		return "", errFake
	}
	switch {
	case strings.Contains(cmd, "grep -o 'pid="):
		if !f.listen || f.pid == 0 {
			return "", nil
		}
		return "pid=" + itoa(f.pid), nil
	case strings.Contains(cmd, "wc -l"):
		if !f.listen {
			return "0\n", nil
		}
		return "1\n", nil
	case strings.Contains(cmd, "/proc/"):
		return f.unit + "\n", nil
	}
	return "", nil
}

type fakeErr struct{}

func (fakeErr) Error() string { return "fake failure" }

var errFake = fakeErr{}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

var _ remote.Exec = (*fakePortExec)(nil)

// TestEnsurePortFreeOwnUnit 自己上次装的服务占着端口不算冲突，
// restart 会让它把端口交出来；这里若误判成冲突，重跑就永远失败。
func TestEnsurePortFreeOwnUnit(t *testing.T) {
	e := &fakePortExec{listen: true, pid: 100, unit: "dukou-client.service"}
	if err := ensurePortFree(e, "dukou-client.service", 7890, 7891); err != nil {
		t.Fatalf("自家服务占端口不应报错: %v", err)
	}
}

// TestEnsurePortFreeForeignUnit 是这条修复的核心用例。
// 云电脑上真实发生过：手工配的 gost-client 占着 7890，
// 新服务 bind 失败退出，但自检连的是旧代理，于是工具报了「全部完成」，
// 出口 IP 却是旧服务器——用户拿着一个连错地方的配置去用。
func TestEnsurePortFreeForeignUnit(t *testing.T) {
	e := &fakePortExec{listen: true, pid: 4242, unit: "gost-client.service"}
	err := ensurePortFree(e, "dukou-client.service", 7890)
	if err == nil {
		t.Fatal("被别的服务占用时必须报错，否则会给出假的成功结果")
	}
	for _, want := range []string{"7890", "gost-client.service", "disable --now"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息应包含 %q，便于用户直接照做：%v", want, err)
		}
	}
}

// TestEnsurePortFreeUnknownOwner 端口有人用但看不到 PID 时也要拦下：
// 拦错只是多问一句，放过则是一个假的「成功」。
func TestEnsurePortFreeUnknownOwner(t *testing.T) {
	e := &fakePortExec{listen: true, pid: 0}
	if err := ensurePortFree(e, "dukou-client.service", 7890); err == nil {
		t.Fatal("看不到占用者时仍应报错提示排查")
	}
}

// TestEnsurePortFreeIdle 端口空着不应当拦。
func TestEnsurePortFreeIdle(t *testing.T) {
	e := &fakePortExec{listen: false}
	if err := ensurePortFree(e, "dukou-client.service", 7890, 7891); err != nil {
		t.Fatalf("端口空闲不应报错: %v", err)
	}
}

// TestSameUnitToleratesSuffix 守住这条真实故障：
// cgroup 里是 "dukou-client.service"，Unit.Name 是 "dukou-client"。
// 早期版本直接字符串比较，于是重跑时被自己的服务拦下，报
// 「端口 7890 已被 dukou-client.service 占用，这个服务不是 dukou 装的」——
// 一句自相矛盾的话，用户完全无从下手。
func TestSameUnitToleratesSuffix(t *testing.T) {
	e := &fakePortExec{listen: true, pid: 16388, unit: "dukou-client.service"}
	if err := ensurePortFree(e, "dukou-client", 7890); err != nil {
		t.Fatalf("自家服务（带 .service 后缀）不应被判成他人: %v", err)
	}
	// 反向也成立：调用方带后缀、读回来不带
	e2 := &fakePortExec{listen: true, pid: 1, unit: "dukou-client"}
	if err := ensurePortFree(e2, "dukou-client.service", 7890); err != nil {
		t.Fatalf("反向比较也应通过: %v", err)
	}
}

// TestEnsurePortFreeUnknownOwnerNotSelf 确认「看不到占用者」不会被当成自己人。
func TestEnsurePortFreeUnknownOwnerNotSelf(t *testing.T) {
	e := &fakePortExec{listen: true, pid: 0, unit: ""}
	if err := ensurePortFree(e, "dukou-client", 7890); err == nil {
		t.Fatal("owner 为空时必须拦下，不能默认放行")
	}
}
