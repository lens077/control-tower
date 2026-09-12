package service

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	v1 "github.com/lens077/control-tower/api/config/v1"
	"github.com/lens077/control-tower/services/config/internal/biz"
	"github.com/lens077/control-tower/services/config/internal/iam"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// requireAdmin 拒绝非 operator 的 machine 主体（纵深防御：iam 白名单已挡，这里再挡一层）。
// 管理员 JWT 与 operator token 放行；operator 的额外限制见各 RPC。
func requireAdmin(ctx context.Context) error {
	p, ok := iam.PrincipalFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("machine tokens cannot manage machine tokens"))
	}
	if p.Machine && !isOperator(p) {
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("machine tokens cannot manage machine tokens"))
	}
	return nil
}

// isOperator 报告主体是否为 role=operator 的 machine token。
func isOperator(p iam.Principal) bool {
	return p.Machine && p.Scope != nil && p.Scope.Operator
}

// operatorPrincipal 返回当前 operator 主体（非 operator 返回 ok=false）。
func operatorPrincipal(ctx context.Context) (iam.Principal, bool) {
	p, ok := iam.PrincipalFromContext(ctx)
	if !ok || !isOperator(p) {
		return iam.Principal{}, false
	}
	return p, true
}

// ListMachineTokens 列出 token 元数据（仅管理员）。
func (s *ConfigService) ListMachineTokens(ctx context.Context, c *connect.Request[v1.ListMachineTokensRequest]) (*connect.Response[v1.ListMachineTokensResponse], error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	tokens, err := s.machineTokens.List(ctx, c.Msg.GetServiceName(), c.Msg.GetEnvironment())
	if err != nil {
		return nil, s.toErr(err)
	}
	pb := make([]*v1.MachineTokenMeta, 0, len(tokens))
	for _, t := range tokens {
		pb = append(pb, toPBMachineToken(t))
	}
	return connect.NewResponse(&v1.ListMachineTokensResponse{Tokens: pb}), nil
}

// IssueMachineToken 签发（管理员或 operator）。明文只在本响应出现一次。
// operator 只能签发 role=service 的 token，不能自我复制。
func (s *ConfigService) IssueMachineToken(ctx context.Context, c *connect.Request[v1.IssueMachineTokenRequest]) (*connect.Response[v1.IssueMachineTokenResponse], error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	role := fromPBRole(c.Msg.GetRole())
	if p, ok := operatorPrincipal(ctx); ok {
		if role == biz.RoleOperator {
			return nil, connect.NewError(connect.CodePermissionDenied,
				errors.New("operator token cannot issue operator tokens"))
		}
		// 最小权限：operator 只能为自身 environment 签发（与其读写范围一致）。
		if c.Msg.GetEnvironment() != p.Scope.Environment {
			return nil, connect.NewError(connect.CodePermissionDenied,
				errors.New("operator token can only issue tokens for its own environment"))
		}
	}
	plaintext, meta, err := s.machineTokens.Issue(ctx,
		c.Msg.GetServiceName(), c.Msg.GetEnvironment(),
		c.Msg.GetAllowedNamespaces(), c.Msg.GetNote(), actor(ctx), role)
	if err != nil {
		if errors.Is(err, biz.ErrInvalidTokenRole) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, s.toErr(err)
	}
	return connect.NewResponse(&v1.IssueMachineTokenResponse{
		Token: plaintext,
		Meta:  toPBMachineToken(meta),
	}), nil
}

// RevokeMachineToken 吊销（管理员或 operator）。已建立的 WatchKeys 流在心跳周期内断开。
// operator 不能吊销 operator token（含自身）：先查目标角色再吊销。
func (s *ConfigService) RevokeMachineToken(ctx context.Context, c *connect.Request[v1.RevokeMachineTokenRequest]) (*connect.Response[v1.RevokeMachineTokenResponse], error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if _, ok := operatorPrincipal(ctx); ok {
		target, err := s.machineTokens.Get(ctx, c.Msg.GetId())
		if err != nil {
			if errors.Is(err, biz.ErrTokenNotFound) {
				return nil, connect.NewError(connect.CodeNotFound, err)
			}
			return nil, s.toErr(err)
		}
		if target.IsOperator() {
			return nil, connect.NewError(connect.CodePermissionDenied,
				errors.New("operator token cannot revoke operator tokens"))
		}
		if p, _ := operatorPrincipal(ctx); target.Environment != p.Scope.Environment {
			return nil, connect.NewError(connect.CodePermissionDenied,
				errors.New("operator token can only revoke tokens in its own environment"))
		}
	}
	if _, err := s.machineTokens.Revoke(ctx, c.Msg.GetId(), actor(ctx)); err != nil {
		if errors.Is(err, biz.ErrTokenNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, s.toErr(err)
	}
	return connect.NewResponse(&v1.RevokeMachineTokenResponse{}), nil
}

// machineScopeGuard 对数据面请求执行 namespace×environment 范围校验。
// 读（GetKey/WatchKeys）与 operator 的写（PutKey）共用同一条规则：
// environment 必须相等，namespace 在白名单内（"*" 为通配）。
func machineScopeGuard(ctx context.Context, namespace, environment string) error {
	p, ok := iam.PrincipalFromContext(ctx)
	if !ok || !p.Machine {
		return nil // 管理员 JWT 主体不受 machine 范围限制
	}
	if p.Scope.AllowsRead(namespace, environment) {
		return nil
	}
	return connect.NewError(connect.CodePermissionDenied,
		errors.New("machine token scope does not cover this namespace/environment"))
}

func toPBMachineToken(t biz.MachineToken) *v1.MachineTokenMeta {
	pb := &v1.MachineTokenMeta{
		Id:                t.ID,
		ServiceName:       t.Service,
		Environment:       t.Environment,
		AllowedNamespaces: t.AllowedNamespaces,
		Note:              t.Note,
		Disabled:          t.Disabled,
		CreatedAt:         timestamppb.New(t.CreatedAt),
		Role:              toPBRole(t.Role),
	}
	if t.RevokedAt != nil {
		pb.RevokedAt = timestamppb.New(*t.RevokedAt)
	}
	if t.LastUsedAt != nil {
		pb.LastUsedAt = timestamppb.New(*t.LastUsedAt)
	}
	return pb
}

func toPBRole(role string) v1.MachineTokenRole {
	switch role {
	case biz.RoleOperator:
		return v1.MachineTokenRole_MACHINE_TOKEN_ROLE_OPERATOR
	default:
		return v1.MachineTokenRole_MACHINE_TOKEN_ROLE_SERVICE
	}
}

// fromPBRole 把 proto 角色映射为 biz 常量；UNSPECIFIED 等价于 SERVICE。
func fromPBRole(role v1.MachineTokenRole) string {
	switch role {
	case v1.MachineTokenRole_MACHINE_TOKEN_ROLE_OPERATOR:
		return biz.RoleOperator
	default:
		return biz.RoleService
	}
}
