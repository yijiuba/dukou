package app

import (
	"errors"
	"fmt"

	"github.com/yijiuba/dukou/internal/deploy"
	"github.com/yijiuba/dukou/internal/store"
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

// DeployServer 从服务器清单发起部署（「先保存后部署」流程）。
//
// in 里允许只带表单上改过的字段：Host/SSHPort/SSHUser 非空就存回清单，
// SSHPassword 非空就更新凭据（留空 = 沿用已保存的密码，不回显不重输）。
// 部署成功后把生成的代理密码与端口写回，状态从 pending 转为 standby。
// 事件流与 Deploy 完全共用（deploy:event），前端渲染逻辑无需分叉。
func (s *Service) DeployServer(id string, in store.ServerInput) (deploy.Result, error) {
	if s.ctx == nil {
		return deploy.Result{}, errors.New("应用尚未就绪，请重启后再试")
	}
	if s.emit == nil {
		return deploy.Result{}, errors.New("界面事件通道未就绪，请重启后再试")
	}

	cur, ok := s.store.Get(id)
	if !ok {
		return deploy.Result{}, fmt.Errorf("找不到服务器 %s，可能已被删除，请刷新列表", id)
	}

	// 表单值优先，空值沿用库里已存的
	if in.Name != "" {
		cur.Name = in.Name
	}
	if in.Host != "" {
		cur.Host = in.Host
	}
	if in.SSHPort > 0 {
		cur.SSHPort = in.SSHPort
	}
	if in.SSHUser != "" {
		cur.SSHUser = in.SSHUser
	}

	// 先把可能修改过的连接信息存回（密码为空表示保持原凭据）
	if _, err := s.store.Update(id, store.ServerInput{
		Name:        cur.Name,
		Flag:        cur.Flag,
		Host:        cur.Host,
		SSHUser:     cur.SSHUser,
		AuthType:    cur.AuthType,
		SSHPort:     cur.SSHPort,
		ProxyPort:   cur.ProxyPort,
		ServerPort:  cur.ServerPort,
		SSHPassword: in.SSHPassword,
	}); err != nil {
		return deploy.Result{}, fmt.Errorf("保存部署前的修改失败：%v", err)
	}

	password := in.SSHPassword
	if password == "" {
		sp, key, err := s.store.SSHCredentials(id)
		if err != nil {
			return deploy.Result{}, fmt.Errorf("读取已保存的 SSH 凭据失败：%v", err)
		}
		if sp == "" && key != "" {
			return deploy.Result{}, errors.New("该服务器保存的是密钥登录，暂不支持从清单一键部署；请到「网络配置」页用私钥路径操作")
		}
		password = sp
	}
	if password == "" {
		return deploy.Result{}, errors.New("没有可用的 SSH 密码：请在部署弹窗里填一次密码，它会加密存回本机")
	}

	// 注意字段套别：Request.SSHPort/SSHUser/SSHPassword 才是 SSH 连接用的；
	// Port/User/Password 是「高级项」（服务端代理监听端口/代理用户名/代理密码），
	// 填错套的后果是 SSH 密码根本没传进去，报「请填写 SSH 密码或私钥」。
	req := deploy.Request{
		ServerHost:  cur.Host,
		SSHPort:     cur.SSHPort,
		SSHUser:     cur.SSHUser,
		SSHPassword: password,
	}
	res, err := s.Deploy(req)
	if err != nil {
		return res, err
	}

	// 部署成功：写回代理通道凭据与端口，状态转备用（可切换）
	if _, err := s.store.Update(id, store.ServerInput{
		Name:          cur.Name,
		Flag:          cur.Flag,
		Host:          cur.Host,
		SSHUser:       cur.SSHUser,
		AuthType:      cur.AuthType,
		SSHPort:       cur.SSHPort,
		ProxyPort:     cur.ProxyPort,
		ServerPort:    res.Port,
		ProxyPassword: res.Password,
	}); err != nil {
		return res, fmt.Errorf("部署成功，但写回代理凭据失败：%v", err)
	}
	if err := s.store.SetStatus(id, "standby"); err != nil {
		return res, fmt.Errorf("部署成功，但更新状态失败：%v", err)
	}
	return res, nil
}
