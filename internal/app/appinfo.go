package app

import (
	"encoding/json"
	"os"
	"runtime"
)

// Version 构建时注入（wails build -ldflags "-X ...internal/app.Version=0.2.0"）。
var Version = "0.2.0-dev"

// AppInfo 应用元信息（§5.1 契约）。
type AppInfo struct {
	Version   string `json:"version"`
	Platform  string `json:"platform"`
	Arch      string `json:"arch"`
	FirstRun  bool   `json:"firstRun"`
	ConfigDir string `json:"configDir"`
}

// appConfig 是 config.json 的落盘结构。
type appConfig struct {
	Onboarded bool `json:"onboarded"`
}

// AppInfo 读取应用信息。
//
// FirstRun 用"读即消费"语义：首次读取返回 true 并把 onboarded=true 落盘，
// 新手引导因此只弹一次——不需要前端再回写。代价是引导弹窗弹出前若进程
// 被杀，下次不再显示；引导可从"设置 → 关于"重看，可接受。
func (s *Service) AppInfo() (AppInfo, error) {
	st := AppInfo{
		Version:   Version,
		Platform:  runtime.GOOS,
		Arch:      runtime.GOARCH,
		ConfigDir: s.store.Dir(),
	}
	raw, err := os.ReadFile(s.configPath())
	if err != nil && !os.IsNotExist(err) {
		// 配置读不动不阻塞启动，按未引导处理
		st.FirstRun = true
		return st, nil
	}
	var cfg appConfig
	if raw != nil {
		if json.Unmarshal(raw, &cfg) != nil {
			cfg = appConfig{}
		}
	}
	if !cfg.Onboarded {
		st.FirstRun = true
		cfg.Onboarded = true
		if data, merr := json.MarshalIndent(cfg, "", "  "); merr == nil {
			_ = atomicWriteJSON(s.configPath(), data)
		}
	}
	return st, nil
}

func (s *Service) configPath() string {
	return s.store.Dir() + string(os.PathSeparator) + "config.json"
}
