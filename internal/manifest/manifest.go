// Package manifest 记录 dukou 对系统做过的每一处变更，用于精确回滚。
//
// 为什么需要它：卸载时「反向猜」是不可靠的——工具可能装过 gost、可能接管过已存在的
// 服务、可能只是加了一条防火墙规则。只有把变更逐条记下来，uninstall 才能做到
// 「改了什么就还什么」，并且在遇到用户原本就有的配置时选择跳过而不是删除。
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// Kind 是变更的类型，决定卸载时怎么撤销。
type Kind string

const (
	KindFile     Kind = "file"     // 普通文件：有 Backup 则还原，无则删除
	KindDir      Kind = "dir"      // 目录：仅在为空时删除
	KindService  Kind = "service"  // systemd 单元：停用并删除单元文件
	KindBinary   Kind = "binary"   // 二进制：只有本次安装的才删除
	KindFirewall Kind = "firewall" // 防火墙规则：通过 Undo 命令撤销
)

// Entry 是一条变更记录。
type Entry struct {
	Kind Kind `json:"kind"`
	// Path 是变更对象：文件路径、服务名、二进制路径。
	// 防火墙规则没有单一路径，用规则本身作 Path 以便去重。
	Path string `json:"path"`
	// Detail 是给人看的一句话描述，doctor / uninstall 会打印它。
	Detail string `json:"detail,omitempty"`
	// Backup 是变更前内容的备份路径。原文件不存在时为空，
	// 此时回滚动作是删除 Path。
	Backup string `json:"backup,omitempty"`
	// Undo 是无法用文件操作撤销的变更所需命令（主要是防火墙规则）。
	Undo string `json:"undo,omitempty"`
	// WasPresent 记录变更前对象是否已存在，用于区分
	// 「dukou 新建的」和「用户原本就有的」。
	WasPresent bool `json:"was_present"`
}

// Manifest 是一台机器上的全部变更。
type Manifest struct {
	Version int     `json:"version"`
	Role    string  `json:"role"`
	Created string  `json:"created"`
	Entries []Entry `json:"entries"`
}

// CurrentVersion 用于将来兼容旧清单。
const CurrentVersion = 1

// DefaultPath 返回清单的默认位置。
func DefaultPath() string {
	if runtime.GOOS == "linux" {
		return "/var/lib/dukou/manifest.json"
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "manifest.json"
	}
	return filepath.Join(dir, "dukou", "manifest.json")
}

// Dir 返回清单所在目录，备份文件也放在它下面的 backup/ 里。
func Dir() string {
	return filepath.Dir(DefaultPath())
}

// BackupDir 返回备份目录。
func BackupDir() string {
	return filepath.Join(Dir(), "backup")
}

// New 创建一个空清单。
func New(role string) *Manifest {
	return &Manifest{
		Version: CurrentVersion,
		Role:    role,
		Created: time.Now().Format(time.RFC3339),
	}
}

// Add 记录一条变更。同一个 (Kind, Path) 重复记录时覆盖旧记录，
// 保证清单幂等——重复执行 server 不会让清单无限增长。
func (m *Manifest) Add(e Entry) {
	for i := range m.Entries {
		if m.Entries[i].Kind == e.Kind && m.Entries[i].Path == e.Path {
			m.Entries[i] = e
			return
		}
	}
	m.Entries = append(m.Entries, e)
}

// Has 判断某个对象是否已经被记录过。
func (m *Manifest) Has(kind Kind, path string) bool {
	for _, e := range m.Entries {
		if e.Kind == kind && e.Path == path {
			return true
		}
	}
	return false
}

// ByKind 返回某一类的全部记录，按记录顺序。
func (m *Manifest) ByKind(kind Kind) []Entry {
	var out []Entry
	for _, e := range m.Entries {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// Load 从路径读取清单。文件不存在时返回空清单而不是错误，
// 因为首次部署时清单本来就不存在。
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Manifest{Version: CurrentVersion, Entries: nil}, nil
		}
		return nil, err
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("清单 %s 格式损坏: %w", path, err)
	}
	if m.Version > CurrentVersion {
		return nil, fmt.Errorf("清单版本 %d 高于本程序支持的 %d，请升级 dukou", m.Version, CurrentVersion)
	}
	return &m, nil
}

// Save 原子写入清单。
// 这里不引用 config 包以避免循环依赖，索引逻辑相同：临时文件 + rename。
func Save(path string, m *Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录 %s 失败: %w", dir, err)
	}

	f, err := os.CreateTemp(dir, ".manifest-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
