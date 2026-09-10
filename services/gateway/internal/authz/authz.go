// Package authz 实现网关粗粒度授权：Casbin 进程内判定（零网络跳）。
//
// 模型形状与旧网关的 model.conf 逐字兼容（policies.csv/model.conf 是新旧网关共用的
// 冻结键，不能改形状）：r = sub(角色), obj(procedure 路径), act(HTTP 方法)；
// keyMatch2 通配、regexMatch 方法、显式 deny 压过 allow、g/g2 角色继承。
//
// 多角色语义（新定义，见 docs/design/auth.md）：对用户的每个角色独立判定，
// 任一角色 allow 即放行；旧网关只取第一个角色的行为不保留。
//
// 热更新：SetPolicies 整体重建 enforcer 后原子替换；非法模型/策略保留旧 enforcer
// （last-known-good——旧网关同语义，历史上防住过坏策略下发）。
//
// 方法列门禁：p 行的 act 列只认字面 POST。model 用 regexMatch 匹配 act，
// 一条图省事写成 `.*` 或 `(GET)|(POST)` 的策略会让「换 HTTP 方法绕授权」
// 只剩 Connect 的 405 兜底——而 Connect 对标了 NO_SIDE_EFFECTS 的方法是放 GET 的。
// 全部下游 procedure 都是 POST（本仓没有任何 RPC 标 NO_SIDE_EFFECTS），
// 所以这里不需要表达力，只需要收窄；将来真要放行 GET，先改这条常量并同步
// ecommerce context/team/proto-design.md 的评审规则。
package authz

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
)

// ErrNotReady 表示策略尚未加载；readyz 就绪条件之一。
var ErrNotReady = errors.New("authz: policies not loaded")

// allowedAct 是 p 行 act 列唯一合法的字面值。Connect procedure 一律 POST。
const allowedAct = "POST"

// pFieldCount 是 p 行去掉 ptype 后的列数：sub, obj, act, eft（与 model.conf 的
// policy_definition 对齐；列数不对 casbin 也会报，但报错信息不指向具体列）。
const pFieldCount = 4

// Enforcer 是可热更新的授权判定器。
type Enforcer struct {
	e atomic.Pointer[casbin.Enforcer]
}

// New 构造空判定器（未加载策略前 Allowed 一律报 ErrNotReady）。
func New() *Enforcer {
	return &Enforcer{}
}

// Ready 报告策略是否已加载。
func (a *Enforcer) Ready() bool {
	return a.e.Load() != nil
}

// SetPolicies 用 model.conf 文本与 policies.csv 文本整体重建判定器。
// 任一解析失败即返回错误并保留旧判定器。
func (a *Enforcer) SetPolicies(modelText, policiesCSV string) error {
	m, err := model.NewModelFromString(modelText)
	if err != nil {
		return fmt.Errorf("authz: parse model: %w", err)
	}
	e, err := casbin.NewEnforcer(m)
	if err != nil {
		return fmt.Errorf("authz: build enforcer: %w", err)
	}
	if err := loadCSV(e, policiesCSV); err != nil {
		return err
	}
	a.e.Store(e)
	return nil
}

// Allowed 判定「任一角色对 procedure+method 是否放行」。
func (a *Enforcer) Allowed(roles []string, procedure, method string) (bool, error) {
	e := a.e.Load()
	if e == nil {
		return false, ErrNotReady
	}
	for _, role := range roles {
		ok, err := e.Enforce(role, procedure, method)
		if err != nil {
			return false, fmt.Errorf("authz: enforce: %w", err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// loadCSV 解析 policies.csv（casbin CSV 方言：p/g/g2 行、# 注释、空行）。
// 未知行类型报错——静默跳过会让策略「看似下发实则没生效」。
// p 行经 validatePolicyRow 做列值校验；任一行不合法整表拒绝加载（调用方保留旧表）。
func loadCSV(e *casbin.Enforcer, csv string) error {
	for i, raw := range strings.Split(csv, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ",")
		for j := range fields {
			fields[j] = strings.TrimSpace(fields[j])
		}
		ptype, rest := fields[0], toAny(fields[1:])
		var err error
		switch ptype {
		case "p":
			if err = validatePolicyRow(fields[1:]); err != nil {
				return fmt.Errorf("authz: policies.csv line %d: %w", i+1, err)
			}
			_, err = e.AddNamedPolicy("p", rest...)
		case "g":
			_, err = e.AddNamedGroupingPolicy("g", rest...)
		case "g2":
			_, err = e.AddNamedGroupingPolicy("g2", rest...)
		default:
			return fmt.Errorf("authz: policies.csv line %d: unknown type %q", i+1, ptype)
		}
		if err != nil {
			return fmt.Errorf("authz: policies.csv line %d: %w", i+1, err)
		}
	}
	return nil
}

// validatePolicyRow 校验 p 行（不含 ptype）：列数固定 4，act 列只认字面 POST。
//
// 拒绝的不只是 `.*`：`GET|POST`、`(GET)|(POST)`、`POST.*`、小写 `post`、
// 单独的 `GET` 全部拒绝——regexMatch 下这些都不是「恰好 POST」，而任何比
// POST 更宽的匹配都会让授权层与执行层对「这是什么操作」的理解分叉。
func validatePolicyRow(fields []string) error {
	if len(fields) != pFieldCount {
		return fmt.Errorf("p row must have %d fields (sub, obj, act, eft), got %d", pFieldCount, len(fields))
	}
	if act := fields[2]; act != allowedAct {
		return fmt.Errorf("p row act column must be literally %q, got %q (wildcards and method alternations are refused)", allowedAct, act)
	}
	return nil
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
