package app

import (
	"fmt"
	"os"
)

// atomicWriteJSON 原子写 JSON：先写临时文件（fsync）再 rename 覆盖，
// 与 internal/store 的原子写同思路——进程中途被杀不留半截文件。
func atomicWriteJSON(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("写入临时文件失败：%v", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("写入临时文件失败：%v", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("刷盘失败：%v", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("关闭文件失败：%v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("替换状态文件失败：%v", err)
	}
	return nil
}
