package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yijiuba/dukou/internal/aitool"
)

// AI 工具（Codex / Claude Code）的检测、安装、启动、卸载。
//
// 与 deploy 里那步「顺带装 Codex」的区别：那一步是部署流水线的一环，
// 只在本机配好代理后装一次；这里是可以反复调用的独立能力，
// 装哪个、装没装、能不能跑、怎么卸，都由用户单独决定。

// AITools 返回全部工具的真实检测状态。
//
// 每次都现场查：用户可能在终端里自己装过、卸过、升级过，
// 缓存一份"上次检测结果"只会让界面和事实越走越远。
func (s *Service) AITools() ([]aitool.State, error) {
	ctx, cancel := context.WithTimeout(s.aiContext(), 30*time.Second)
	defer cancel()
	return aitool.DetectAll(ctx, aitool.SystemProbe(), s.aiProxyAddr()), nil
}

// AIToolInstall 安装一款 AI 工具。
//
// 长任务：进度经 ai:progress 事件推给界面，返回值只是最终结果。
// 同一时刻只允许一个安装/卸载——npm 全局目录是共享的，
// 两个 npm install 并发跑会互相锁住，表现是界面卡死没有进度。
func (s *Service) AIToolInstall(id string) (aitool.Result, error) {
	t, err := aiToolByID(id)
	if err != nil {
		return aitool.Result{}, err
	}
	if !s.muAI.TryLock() {
		return aitool.Result{}, errors.New("上一个安装或卸载还在进行，请等它结束再试")
	}
	defer s.muAI.Unlock()

	ctx, cancel := context.WithTimeout(s.aiContext(), 15*time.Minute)
	defer cancel()

	return aitool.Install(ctx, aitool.SystemProbe(), t, s.aiProxyAddr(), s.aiEmitter(id))
}

// AIToolUninstall 卸载一款 AI 工具。只卸 npm 全局包，不删别处的副本。
func (s *Service) AIToolUninstall(id string) (aitool.Result, error) {
	t, err := aiToolByID(id)
	if err != nil {
		return aitool.Result{}, err
	}
	if !s.muAI.TryLock() {
		return aitool.Result{}, errors.New("上一个安装或卸载还在进行，请等它结束再试")
	}
	defer s.muAI.Unlock()

	ctx, cancel := context.WithTimeout(s.aiContext(), 5*time.Minute)
	defer cancel()

	return aitool.Uninstall(ctx, aitool.SystemProbe(), t, s.aiEmitter(id))
}

// AIToolLaunch 在新终端窗口里启动工具（带代理环境变量）。
func (s *Service) AIToolLaunch(id string) (aitool.Result, error) {
	t, err := aiToolByID(id)
	if err != nil {
		return aitool.Result{}, err
	}
	ctx, cancel := context.WithTimeout(s.aiContext(), 30*time.Second)
	defer cancel()

	return aitool.Launch(ctx, aitool.SystemProbe(), t, s.aiProxyAddr(), s.store.Dir(), s.aiEmitter(id))
}

// aiToolByID 校验工具 ID，错误文案直接给用户看。
func aiToolByID(id string) (aitool.Tool, error) {
	if id == "" {
		return aitool.Tool{}, errors.New("请指定要操作的 AI 工具")
	}
	t, ok := aitool.Lookup(id)
	if !ok {
		return aitool.Tool{}, fmt.Errorf("暂不支持这个工具（%s），当前可用：codex、claude", id)
	}
	return t, nil
}

// aiEmitter 把进度转成界面事件。事件名 ai:progress，载荷带 id/stage/msg。
func (s *Service) aiEmitter(id string) aitool.Emitter {
	return func(st aitool.Step) {
		if s.emit == nil {
			return
		}
		s.emit("ai:progress", map[string]interface{}{
			"id":    id,
			"stage": st.Stage,
			"msg":   st.Msg,
		})
	}
}

// aiProxyAddr 返回本机代理地址；未连接时返回空串。
//
// 空串不是错误：检测工具不需要网络，安装才需要。让调用方自己决定
// 「没代理时是拦下来还是先试试直连」。
func (s *Service) aiProxyAddr() string {
	st, err := s.ProxyStatus()
	if err != nil || !st.Connected || st.LocalPort == 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d", st.LocalPort)
}

// aiContext 保证 nil ctx 时也能跑检测（应用尚未 Startup 的场景）。
func (s *Service) aiContext() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}
