//go:build !dev

package main

// 生产构建不带 dev tag，绝不暴露 CDP 调试端口。
var debug = false
