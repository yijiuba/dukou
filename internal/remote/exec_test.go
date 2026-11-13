package remote

import (
	"strings"
	"sync"
	"testing"
)

// TestSyncBufferConcurrentWrites 守住一次真机事故。
//
// 起点是把同一个 bytes.Buffer 同时赋给 Stdout 和 Stderr：os/exec 与
// x/crypto/ssh 都会为每条流各起一个 goroutine 拷贝，两个 goroutine
// 并发写同一个非并发安全的 buffer，输出会随机丢失，而 err 仍为 nil。
//
// 当时的症状是：远端 id -u 十次里有两三次返回空串，工具据此认定
// "SSH 账号不是 root" 并中止部署。这种错在本地跑单测永远碰不到，
// 只能靠"用并发写去撞它"来回归。
func TestSyncBufferConcurrentWrites(t *testing.T) {
	const (
		writers = 2
		rounds  = 500
		line    = "0123456789"
	)

	var buf syncBuffer
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < rounds; j++ {
				if _, err := buf.Write([]byte(line)); err != nil {
					t.Errorf("写入失败: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	got := buf.String()
	want := writers * rounds * len(line)
	if len(got) != want {
		t.Fatalf("并发写入丢失：期望 %d 字节，实际 %d 字节", want, len(got))
	}
	if strings.Count(got, line) != writers*rounds {
		t.Fatalf("%q 被撕裂，写入了不完整的片段", line)
	}
}

// TestSyncBufferStringConcurrent 验证读取与写入可以并行，
// 因为超时分支会在写入 goroutine 仍在跑时调用 String()。
func TestSyncBufferStringConcurrent(t *testing.T) {
	var buf syncBuffer

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_, _ = buf.Write([]byte("x"))
		}
	}()

	for i := 0; i < 200; i++ {
		_ = buf.String()
	}
	wg.Wait()

	if got := buf.String(); len(got) != 200 {
		t.Fatalf("期望 200 字节，实际 %d", len(got))
	}
}

// TestRewriteSudo 守住 sudo 的改写位置。
//
// 云电脑上真实踩过：写 sysctl 配置的命令是
//
//	echo '…' | base64 -d > /etc/sysctl.d/99-dukou.conf
//
// 而 ">" 由非 root shell 解释，root 只覆盖了 echo 半句，配置一个字没写进去。
// 后来改成 "| sudo tee"，但那个 sudo 不在行首——只给行首注入密码的实现
// 会让它卡在等密码上。所以这里要断言：管道中间、&& 之后的 sudo 也必须被改写。
func TestRewriteSudo(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sudo true", "sudo -A true"},
		{"echo 'eA==' | base64 -d | sudo tee /etc/x", "echo 'eA==' | base64 -d | sudo -A tee /etc/x"},
		{"systemctl daemon-reload && sudo systemctl restart x", "systemctl daemon-reload && sudo -A systemctl restart x"},
		{"(sudo cat /proc/1/cgroup)", "(sudo -A cat /proc/1/cgroup)"},
		// 字面量不能被误伤
		{"dukou deploy --sudo-password x", "dukou deploy --sudo-password x"},
		{"echo sudo", "echo sudo -A"},
	}
	for _, c := range cases {
		if got := rewriteSudo(c.in); got != c.want {
			t.Errorf("rewriteSudo(%q)\n得到: %q\n期望: %q", c.in, got, c.want)
		}
	}
}
