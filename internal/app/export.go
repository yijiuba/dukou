package app

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// 脱敏清单的列（顺序即 CSV 表头）。密码与私钥永不出现在这里。
var exportColumns = []string{"名称", "地址", "SSH 端口", "SSH 账号", "认证方式", "服务器端口", "本机端口", "状态", "最近体检"}

// ExportServers 把服务器清单导出为脱敏的 CSV 或 JSON（不含任何凭据）。
//
// 与 .dukoubak 的分工：那个是"能再导入的加密备份"，这个是"给人看的清单"——
// 贴工单、放进表格、发给同事都用得着，所以密码一律 ******。
func (s *Service) ExportServers(path, format string) (int, error) {
	if strings.TrimSpace(path) == "" {
		return 0, errors.New("请选择保存位置")
	}
	list := s.store.List()

	switch strings.ToLower(strings.TrimPrefix(format, ".")) {
	case "csv":
		if !strings.EqualFold(filepath.Ext(path), ".csv") {
			path += ".csv"
		}
		rows := make([][]string, 0, len(list)+1)
		rows = append(rows, exportColumns)
		for _, srv := range list {
			rows = append(rows, []string{
				srv.Name, srv.Host, strconv.Itoa(srv.SSHPort), srv.SSHUser, srv.AuthType,
				strconv.Itoa(srv.ServerPort), strconv.Itoa(srv.ProxyPort), srv.Status, srv.LastCheck,
			})
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return 0, fmt.Errorf("创建文件失败：%v", err)
		}
		defer f.Close()
		_, _ = f.WriteString("\xEF\xBB\xBF") // Excel 打开中文不乱码
		w := csv.NewWriter(f)
		if err := w.WriteAll(rows); err != nil {
			return 0, fmt.Errorf("写入 CSV 失败：%v", err)
		}
		w.Flush()
		if err := w.Error(); err != nil {
			return 0, fmt.Errorf("写入 CSV 失败：%v", err)
		}
		return len(list), nil

	case "json":
		if !strings.EqualFold(filepath.Ext(path), ".json") {
			path += ".json"
		}
		type row struct {
			Name       string `json:"name"`
			Host       string `json:"host"`
			SSHPort    int    `json:"sshPort"`
			SSHUser    string `json:"sshUser"`
			AuthType   string `json:"authType"`
			ServerPort int    `json:"serverPort"`
			ProxyPort  int    `json:"proxyPort"`
			Status     string `json:"status"`
			LastCheck  string `json:"lastCheck"`
			Password   string `json:"password"`
		}
		out := make([]row, 0, len(list))
		for _, srv := range list {
			out = append(out, row{
				Name: srv.Name, Host: srv.Host, SSHPort: srv.SSHPort, SSHUser: srv.SSHUser,
				AuthType: srv.AuthType, ServerPort: srv.ServerPort, ProxyPort: srv.ProxyPort,
				Status: srv.Status, LastCheck: srv.LastCheck, Password: "******",
			})
		}
		data, err := json.MarshalIndent(map[string]interface{}{
			"exportedAt": time.Now().Format(time.RFC3339),
			"appVersion": Version,
			"note":       "密码已脱敏；需要可再导入的完整备份请用「一键备份」（.dukoubak）",
			"servers":    out,
		}, "", "  ")
		if err != nil {
			return 0, fmt.Errorf("生成 JSON 失败：%v", err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return 0, fmt.Errorf("写入 JSON 失败：%v", err)
		}
		return len(list), nil

	default:
		return 0, fmt.Errorf("不支持的导出格式 %q（只支持 csv / json）", format)
	}
}
