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

// requireAdmin 拒绝 machine 主体（纵深防御：iam 白名单已挡，这里再挡一层）。
func requireAdmin(ctx context.Context) error {
	p, ok := iam.PrincipalFromContext(ctx)
	if !ok || p.Machine {
		return connect.NewError(connect.CodePermissionDenied,
			errors.New("machine tokens cannot manage machine tokens"))
	}
	return nil
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

// IssueMachineToken 签发（仅管理员）。明文只在本响应出现一次。
func (s *ConfigService) IssueMachineToken(ctx context.Context, c *connect.Request[v1.IssueMachineTokenRequest]) (*connect.Response[v1.IssueMachineTokenResponse], error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	plaintext, meta, err := s.machineTokens.Issue(ctx,
		c.Msg.GetServiceName(), c.Msg.GetEnvironment(),
		c.Msg.GetAllowedNamespaces(), c.Msg.GetNote(), actor(ctx))
	if err != nil {
		return nil, s.toErr(err)
	}
	return connect.NewResponse(&v1.IssueMachineTokenResponse{
		Token: plaintext,
		Meta:  toPBMachineToken(meta),
	}), nil
}

// RevokeMachineToken 吊销（仅管理员）。已建立的 WatchKeys 流在心跳周期内断开。
func (s *ConfigService) RevokeMachineToken(ctx context.Context, c *connect.Request[v1.RevokeMachineTokenRequest]) (*connect.Response[v1.RevokeMachineTokenResponse], error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if _, err := s.machineTokens.Revoke(ctx, c.Msg.GetId(), actor(ctx)); err != nil {
		if errors.Is(err, biz.ErrTokenNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, s.toErr(err)
	}
	return connect.NewResponse(&v1.RevokeMachineTokenResponse{}), nil
}

// machineScopeGuard 对数据面读请求执行 namespace×environment 范围校验。
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
	}
	if t.RevokedAt != nil {
		pb.RevokedAt = timestamppb.New(*t.RevokedAt)
	}
	if t.LastUsedAt != nil {
		pb.LastUsedAt = timestamppb.New(*t.LastUsedAt)
	}
	return pb
}


