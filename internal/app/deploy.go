package app

import (
	"errors"
	"fmt"

	"github.com/yijiuba/dukou/internal/deploy"
)

// Deploy 执行完整部署流水线。进度经注入的 EmitFunc 推给界面（事件名 deploy:event）。
//
// 失败语义（§5.3）：部署中的失败不改用 error 抛到界面，而是 deploy:event 的
// step_fail + Detail；这里 error 只兜住"根本没跑起来"的情况。
func (s *Service) Deploy(req deploy.Request) (deploy.Result, error) {
	if s.ctx == nil {
		return deploy.Result{}, errors.New("应用尚未就绪，请重启后再试")
	}
	if s.emit == nil {
		return deploy.Result{}, errors.New("界面事件通道未就绪，请重启后再试")
	}
	emit := func(ev deploy.Event) {
		s.emit("deploy:event", ev)
	}
	res, err := deploy.Run(s.ctx, req, emit)
	if err != nil {
		// deploy.Run 返回的 error 已是中文可读文案，这里补一层来源标注
		return res, fmt.Errorf("部署未完成：%v", err)
	}
	return res, nil
}
