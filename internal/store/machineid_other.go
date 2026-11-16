//go:build !windows

package store

import (
	"errors"
	"os"
)

// machineID 取类 Unix 系统的机器码。
// Linux：/etc/machine-id（systemd）或 /var/lib/dbus/machine-id；
// 其他平台退化为 hostname + 用户名。
func machineID() (string, error) {
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			return string(b), nil
		}
	}
	host, herr := os.Hostname()
	if herr != nil {
		return "", errors.New("无法获取本机标识（machine-id 与 hostname 均失败）")
	}
	return host + ":" + os.Getenv("USER"), nil
}
