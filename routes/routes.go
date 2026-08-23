// Package routes 以 go:embed 导出网关路由表的可审查模板。
//
// 两个消费方：
//  1. control-tower 自身：发布检查单用它与 Config Center 线上键（gateway/<env>/routes.yaml）做 diff；
//  2. ecommerce 的 structcheck：import 本包与 .service-matrix.yaml 双向核对网关前缀与后端注册名。
//
// 拓扑真相源仍是 ecommerce 的 .service-matrix.yaml；本包只是路由模板的机器可读载体。
// 路由变更必须同 PR 升级 ecommerce 对本模块的依赖版本，否则 structcheck 变红。
package routes

import (
	"embed"
	"fmt"
)

//go:embed dev.yaml pre.yaml
var fs embed.FS

// Env 返回指定环境的路由表模板原文。
func Env(env string) ([]byte, error) {
	b, err := fs.ReadFile(env + ".yaml")
	if err != nil {
		return nil, fmt.Errorf("routes: unknown env %q: %w", env, err)
	}
	return b, nil
}

// Envs 返回内嵌的环境清单。
func Envs() []string {
	return []string{"dev", "pre"}
}
