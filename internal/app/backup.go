package app

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/yijiuba/dukou/internal/store"
)

// SuggestedBackupName 返回建议的备份文件名（前端另存对话框用）。
func (s *Service) SuggestedBackupName() string {
	return store.SuggestedBackupName()
}

// ExportBackup 把服务器清单、凭据与偏好打包成 .dukoubak（§5.1 / §八 M3）。
//
// path 由前端另存对话框给出；password 是用户为这个备份文件专门设的口令。
// 口令不落盘、不进日志——它在生成密钥的那一刻用完即弃。
func (s *Service) ExportBackup(path, password string) (store.BackupSummary, error) {
	if strings.TrimSpace(path) == "" {
		return store.BackupSummary{}, errors.New("请选择备份文件的保存位置")
	}
	if !strings.EqualFold(filepath.Ext(path), ".dukoubak") {
		path += ".dukoubak"
	}
	return s.store.ExportBackup(path, password, Version)
}

// ImportBackup 从 .dukoubak 恢复配置。
//
// 导入按服务器 ID 合并：备份里有的覆盖/新增，本机独有的保持不动，
// 因此"换机恢复"与"本机增量恢复"走同一条路。导入前自动留快照。
func (s *Service) ImportBackup(path, password string) (store.BackupSummary, error) {
	if strings.TrimSpace(path) == "" {
		return store.BackupSummary{}, errors.New("请选择要导入的备份文件")
	}
	sum, err := s.store.ImportBackup(path, password)
	if err != nil {
		return sum, err
	}
	// 连接状态是导出时的"现场"，不跟着备份走：换台机器导进来就"已连接"
	// 只会让用户莫名其妙。这里只提醒，不自动断开（断开要有用户明确动作）。
	if st, perr := s.ProxyStatus(); perr == nil && st.Connected {
		sum.Warnings = append(sum.Warnings, "当前处于连接状态：导入的配置在下次连接时生效，建议先断开再重连")
	}
	return sum, nil
}
