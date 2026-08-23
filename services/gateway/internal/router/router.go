// Package router 实现「一级 proto 包名 → 后端目标」的不可变路由表。
//
// 匹配语义（沿旧网关收窄，见 docs/design/architecture.md）：
//   - Connect procedure 路径形如 /<package>.<Service>/<Method>，取路径首段第一个「.」之前的子串作包名键；
//   - 首段不含「.」的路径不是 Connect 路由，一律未命中（/username/x 不会误命中 user）；
//   - 精确比对、大小写敏感；路径长度超限直接未命中。
//
// 热更新由上层以整表替换实现（last-known-good 语义），Table 本身构建后只读。
package router

import (
	"fmt"
	"strings"
	"time"

	confv1 "github.com/lens077/control-tower/services/gateway/internal/conf/v1"
)

// MaxPathLen 是允许进入路由匹配的路径长度上限；超长路径在这里提前拒绝。
const MaxPathLen = 512

// Route 是一条已解析的路由。
type Route struct {
	// Package 是一级 proto 包名。
	Package string
	// Target 是后端目标（discovery:///<注册名> 或 direct://<host:port>）。
	Target string
	// Timeout 是路由级总超时。
	Timeout time.Duration
}

// Table 是一份不可变路由表。
type Table struct {
	byPackage map[string]Route
	anonymous map[string]struct{}
	online    map[string]struct{}
}

// Build 从已通过 protovalidate 校验的 RouteConfig 构建路由表。
// 重复包名视为配置错误：静默去重会让「改了配置没生效」难以排查。
func Build(cfg *confv1.RouteConfig) (*Table, error) {
	if cfg == nil {
		return nil, fmt.Errorf("router: nil RouteConfig")
	}
	t := &Table{
		byPackage: make(map[string]Route, len(cfg.GetRoutes())),
		anonymous: make(map[string]struct{}, len(cfg.GetAnonymous())),
		online:    make(map[string]struct{}),
	}
	for _, r := range cfg.GetRoutes() {
		pkg := r.GetPackage()
		if _, dup := t.byPackage[pkg]; dup {
			return nil, fmt.Errorf("router: duplicate package %q", pkg)
		}
		t.byPackage[pkg] = Route{
			Package: pkg,
			Target:  r.GetTarget(),
			Timeout: r.GetTimeout().AsDuration(),
		}
	}
	for _, p := range cfg.GetAnonymous() {
		t.anonymous[p] = struct{}{}
	}
	for _, p := range cfg.GetAuth().GetOnlineCheckProcedures() {
		t.online[p] = struct{}{}
	}
	return t, nil
}

// Resolve 按路径解析路由。返回 false 表示未命中（未知包、非 Connect 形态路径或超长路径）。
func (t *Table) Resolve(path string) (Route, bool) {
	pkg, ok := packageOf(path)
	if !ok {
		return Route{}, false
	}
	r, ok := t.byPackage[pkg]
	return r, ok
}

// IsAnonymous 按完整 procedure 路径判定是否在匿名清单（authn 与 authz 共用）。
func (t *Table) IsAnonymous(procedure string) bool {
	_, ok := t.anonymous[procedure]
	return ok
}

// NeedsOnlineCheck 判定 procedure 是否要求实时在线校验。
func (t *Table) NeedsOnlineCheck(procedure string) bool {
	_, ok := t.online[procedure]
	return ok
}

// DiscoveryTargets 返回全部 discovery:/// 目标的注册名（含重复；调用方自行去重），
// 供装配层建立 Consul Watch。
func (t *Table) DiscoveryTargets() []string {
	const prefix = "discovery:///"
	var out []string
	for _, r := range t.byPackage {
		if strings.HasPrefix(r.Target, prefix) {
			out = append(out, r.Target[len(prefix):])
		}
	}
	return out
}

// packageOf 提取路径首段第一个「.」之前的包名。
func packageOf(path string) (string, bool) {
	if len(path) < 2 || len(path) > MaxPathLen || path[0] != '/' {
		return "", false
	}
	seg := path[1:]
	if i := strings.IndexByte(seg, '/'); i >= 0 {
		seg = seg[:i]
	}
	dot := strings.IndexByte(seg, '.')
	if dot <= 0 {
		// 首段没有「.」（或以「.」开头）：不是 Connect 路由。
		return "", false
	}
	return seg[:dot], true
}
