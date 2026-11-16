// Package aitool 管理本机的 AI 命令行工具：检测、安装、启动、卸载。
//
// 设计上的三条底线：
//  1. **只装自己装的**：安装前查、安装后验、卸载后再查一次。命令还在 PATH 里
//     就如实说「可能来自其它安装位置」，不假装卸干净了。
//  2. **不碰登录**：Codex / Claude Code 首次运行要浏览器 OAuth，必须用户本人
//     点授权，工具替他点不了，也不该伪造一个"已登录"。
//  3. **不代装桌面 App**：ChatGPT 之类的厂商分发安装包有签名与自动更新链路，
//     代装只会和官方更新器打架，这里只给教程。
package aitool

import "fmt"

// Tool 是一款可管理的 AI 命令行工具。
type Tool struct {
	ID      string `json:"id"`      // 稳定标识，界面与事件里用
	Name    string `json:"name"`    // 展示名
	Vendor  string `json:"vendor"`  // 厂商
	NPM     string `json:"npm"`     // npm 包名；空表示不由本包安装
	Bin     string `json:"bin"`     // 可执行文件名（检测与启动用）
	MinNode int    `json:"minNode"` // 需要的 Node 主版本下限，0 表示无要求
	Desc    string `json:"desc"`
}

// Registry 是本版本支持的工具清单。
//
// 只列能真正装干净的：npm 全局安装 → 一个可执行文件 → 可卸载。
// Gemini CLI 也满足，但不同账号体系下的登录态会互相干扰，等 v0.4 一起做。
var Registry = []Tool{
	{
		ID:      "codex",
		Name:    "Codex",
		Vendor:  "OpenAI",
		NPM:     "@openai/codex",
		Bin:     "codex",
		MinNode: 18,
		Desc:    "OpenAI 官方终端编程助手，能读写项目文件、执行命令、跑测试。",
	},
	{
		ID:      "claude",
		Name:    "Claude Code",
		Vendor:  "Anthropic",
		NPM:     "@anthropic-ai/claude-code",
		Bin:     "claude",
		MinNode: 18,
		Desc:    "Anthropic 官方终端助手，长上下文适合整文件重构与读大段日志。",
	},
}

// Lookup 按 ID 取工具。
func Lookup(id string) (Tool, bool) {
	for _, t := range Registry {
		if t.ID == id {
			return t, true
		}
	}
	return Tool{}, false
}

// MustLookup 供已确认存在的调用点使用，找不到直接 panic（属于编程错误）。
func MustLookup(id string) Tool {
	t, ok := Lookup(id)
	if !ok {
		panic(fmt.Sprintf("aitool: 未知工具 %q", id))
	}
	return t
}
