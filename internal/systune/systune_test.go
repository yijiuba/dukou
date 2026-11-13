package systune

import (
	"strings"
	"testing"
)

// stubExec 按命令关键字返回预设输出，用来覆盖 Apply 的各条分支。
type stubExec struct {
	// congestion 是回读 sysctl 时假装得到的值
	congestion string
	// failOn 命中该子串的命令返回错误
	failOn string
	calls  []string
}

func (s *stubExec) Label() string { return "stub" }
func (s *stubExec) Close() error  { return nil }

func (s *stubExec) Run(cmd string) (string, error) {
	s.calls = append(s.calls, cmd)
	if s.failOn != "" && strings.Contains(cmd, s.failOn) {
		return "boom", errStub
	}
	if strings.Contains(cmd, "sysctl -n net.ipv4.tcp_congestion_control") {
		return s.congestion + "\n", nil
	}
	return "", nil
}

type stubErr struct{}

func (stubErr) Error() string { return "stub failure" }

var errStub = stubErr{}

// TestConfHasBBR 守住配置内容本身：少了这两行，其余缓冲区参数都白搭。
func TestConfHasBBR(t *testing.T) {
	c := Conf()
	for _, want := range []string{
		"net.core.default_qdisc = fq",
		"net.ipv4.tcp_congestion_control = bbr",
		"net.core.rmem_max = 67108864",
		"net.core.wmem_max = 67108864",
		"net.ipv4.tcp_mtu_probing = 1",
	} {
		if !strings.Contains(c, want) {
			t.Errorf("配置缺少 %q", want)
		}
	}
}

// TestApplyVerifiesReadBack 是最重要的一条：
// 写进去不等于生效。内核不认某个键时会静默忽略，
// 所以必须回读确认，否则工具会报"已优化"而实际仍是 cubic。
func TestApplyVerifiesReadBack(t *testing.T) {
	e := &stubExec{congestion: "cubic"}
	if _, err := Apply(e, false); err == nil {
		t.Fatal("回读发现仍是 cubic 时应当报错，否则等于假装成功")
	} else if !strings.Contains(err.Error(), "cubic") {
		t.Errorf("错误信息应带上实际值，方便定位：%v", err)
	}
}

// TestApplySudoPrefix 确认本机普通用户场景下每条命令都带 sudo。
// 漏掉任何一条，那个文件就写不进去，而失败点在远端很难看清。
func TestApplySudoPrefix(t *testing.T) {
	e := &stubExec{congestion: "bbr"}
	msg, err := Apply(e, true)
	if err != nil {
		t.Fatalf("不应失败: %v", err)
	}
	if !strings.Contains(msg, "bbr") {
		t.Errorf("期望返回实际算法，得到 %q", msg)
	}
	for _, c := range e.calls {
		if !strings.HasPrefix(c, "sudo ") {
			t.Errorf("命令没带 sudo 前缀: %q", c)
		}
	}
}

// TestApplyPropagatesWriteFailure 确认写文件失败时立刻停下，
// 而不是继续跑 sysctl 然后报告一个假的好结果。
func TestApplyPropagatesWriteFailure(t *testing.T) {
	e := &stubExec{congestion: "bbr", failOn: ConfPath}
	if _, err := Apply(e, false); err == nil {
		t.Fatal("写配置失败时必须返回错误")
	}
}
