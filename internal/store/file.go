package store

import (
	"fmt"
	"os"
	"path/filepath"
)

// atomicWrite 原子写：先写同目录临时文件（fsync 落盘），再 rename 覆盖目标。
// 进程中途被杀最多留下一个 .tmp 残件，目标文件永远完整。
// Windows 下 os.Rename 对已存在目标走 MoveFileEx(REPLACE_EXISTING)，可直接覆盖。
func atomicWrite(path string, data []byte) error {
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
		return fmt.Errorf("替换配置文件失败：%v", err)
	}
	return nil
}

// dirWritable 探测配置目录是否可写（首次运行自检用）。
func dirWritable(dir string) error {
	probe := filepath.Join(dir, ".write-probe")
	if err := atomicWrite(probe, []byte("ok")); err != nil {
		return err
	}
	return os.Remove(probe)
}
