package cmd

// runRotatePassword 生成 16 位随机密码并同步到两端配置。
// v0.1 未实现：需要先有 /etc/dukou.conf 与两端服务，才能安全轮换。
func runRotatePassword(args []string) error {
	return notImplemented("rotate-password")
}
