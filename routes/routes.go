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
	"strings"

	"gopkg.in/yaml.v3"
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

// Entry 是路由模板的一条解析结果（供 ecommerce structcheck 这类消费方做拓扑核对）。
type Entry struct {
	// Package 是一级 proto 包名。
	Package string `yaml:"package"`
	// Target 形如 discovery:///<consul 注册名>。
	Target string `yaml:"target"`
}

// Parsed 是路由模板的机器可读视图。
type Parsed struct {
	Routes    []Entry  `yaml:"routes"`
	Anonymous []string `yaml:"anonymous"`
	// Guest 是匿名购物的访客清单（B 级：不验 JWT，但网关注入访客身份）。
	// 与 Anonymous 语义互斥，网关 Build 时会拒绝同一路径同时出现在两者。
	Guest []string `yaml:"guest"`
}

// Parse 解析指定环境的路由模板。
// 只做形状解析，不做业务校验——完整校验（protovalidate）在网关 loader 内执行。
func Parse(env string) (Parsed, error) {
	raw, err := Env(env)
	if err != nil {
		return Parsed{}, err
	}
	var p Parsed
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return Parsed{}, fmt.Errorf("routes: parse %s: %w", env, err)
	}
	return p, nil
}

// DiscoveryTarget 返回 Entry 的 Consul 注册名；非 discovery 目标返回空串。
func (e Entry) DiscoveryTarget() string {
	const prefix = "discovery:///"
	if strings.HasPrefix(e.Target, prefix) {
		return e.Target[len(prefix):]
	}
	return ""
}
