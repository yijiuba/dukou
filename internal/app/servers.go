package app

import "github.com/yijiuba/dukou/internal/store"

// ListServers 返回服务器清单（不含任何机密字段）。
func (s *Service) ListServers() ([]store.Server, error) {
	return s.store.List(), nil
}

// AddServer 新增服务器。密码/私钥只进加密库，不回显、不进清单文件。
func (s *Service) AddServer(in store.ServerInput) (store.Server, error) {
	return s.store.Add(in)
}

// UpdateServer 编辑服务器。SSHPassword/SSHKey 留空表示保持原凭据。
func (s *Service) UpdateServer(id string, in store.ServerInput) (store.Server, error) {
	return s.store.Update(id, in)
}

// RemoveServer 删除服务器（含加密凭据）。
func (s *Service) RemoveServer(id string) error {
	return s.store.Remove(id)
}
