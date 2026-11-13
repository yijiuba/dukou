package remote

import (
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHConfig 是登录服务器的凭据。密码与私钥二选一。
type SSHConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	// Key 是 OpenSSH 格式的私钥全文（含 BEGIN/END 行），可选。
	Key string
	// Timeout 是建连超时，零值取 15 秒。
	Timeout time.Duration
}

// SSH 通过 SSH 在远端执行命令。
type SSH struct {
	cfg    SSHConfig
	client *ssh.Client
}

// DialSSH 建立连接。主机密钥不做校验：
// 首次连接时用户还没机会确认指纹，强制校验只会把小白挡在门外；
// 代价是理论上可被中间人冒充，属于明知的取舍。
func DialSSH(cfg SSHConfig) (*SSH, error) {
	if cfg.Host == "" {
		return nil, fmt.Errorf("服务器地址不能为空")
	}
	if cfg.Port == 0 {
		cfg.Port = 22
	}
	if cfg.User == "" {
		cfg.User = "root"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}

	var auths []ssh.AuthMethod
	if cfg.Key != "" {
		signer, err := ssh.ParsePrivateKey([]byte(cfg.Key))
		if err != nil {
			return nil, fmt.Errorf("私钥解析失败（需要 OpenSSH 格式，含 BEGIN/END 行）: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if cfg.Password != "" {
		auths = append(auths, ssh.Password(cfg.Password))
	}
	if len(auths) == 0 {
		return nil, fmt.Errorf("请填写 SSH 密码或私钥")
	}

	client, err := ssh.Dial("tcp", net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port)), &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         cfg.Timeout,
	})
	if err != nil {
		return nil, explainDialError(err, cfg)
	}
	return &SSH{cfg: cfg, client: client}, nil
}

func (s *SSH) Label() string { return "服务器" }

func (s *SSH) Close() error { return s.client.Close() }

// Run 在远端执行命令。每次新建一个 session，避免上次的退出状态污染本次。
func (s *SSH) Run(cmd string) (string, error) {
	sess, err := s.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("建立会话失败（连接可能已断开）: %w", err)
	}
	defer sess.Close()

	var buf syncBuffer
	sess.Stdout = &buf
	sess.Stderr = &buf

	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	select {
	case err := <-done:
		out := buf.String()
		if err != nil {
			return out, fmt.Errorf("%s: %w", firstLine(out), err)
		}
		return out, nil
	case <-time.After(5 * time.Minute):
		_ = sess.Signal(ssh.SIGKILL)
		return buf.String(), fmt.Errorf("远端命令超时（5 分钟）：%s", cmd)
	}
}

// explainDialError 把 SSH 的英文报错翻译成能指导行动的中文。
// 这一步很关键：小白看到 "unable to authenticate" 只会懵。
func explainDialError(err error, cfg SSHConfig) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unable to authenticate"),
		strings.Contains(msg, "no supported methods remain"):
		return fmt.Errorf("账号或密码不对（用户 %s）。如果服务器禁用了密码登录，"+
			"请改用私钥；也可以在宝塔面板的「终端」里确认一下 root 密码", cfg.User)
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("服务器 %s:%d 拒绝了连接。检查是否改过 SSH 端口，"+
			"或阿里云安全组没有放行该端口", cfg.Host, cfg.Port)
	case strings.Contains(msg, "i/o timeout"), strings.Contains(msg, "connection timed out"):
		return fmt.Errorf("连不上 %s:%d（超时）。检查 IP 是否正确、服务器是否在运行、"+
			"安全组是否放行 SSH 端口", cfg.Host, cfg.Port)
	case strings.Contains(msg, "no such host"):
		return fmt.Errorf("域名 %s 解析不到，请直接填 IP", cfg.Host)
	}
	return fmt.Errorf("SSH 连接失败: %w", err)
}
