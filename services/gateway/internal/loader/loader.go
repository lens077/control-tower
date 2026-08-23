// Package loader 负责网关五键动态配置的获取、校验、应用与热更新。
//
// 五键（Config Center namespace=gateway，见 docs/design/architecture.md）：
//   - routes.yaml         路由表 + 匿名清单 + online_check + CORS（新键；旧网关 config.yaml 冻结）
//   - secrets/public.pem  JWT 验签公钥（与旧网关共用，只读）
//   - policies/policies.csv、policies/model.conf  Casbin（与旧网关共用，只读）
//   - auth/revocations.yaml  撤销名单（新键；首次拉取 NotFound 容忍为空表）
//
// 语义：
//   - 启动：前四键必须成功拉到并通过校验，否则快速失败（旧网关纪律）；
//   - 热更新：任何非法/删除事件保留 last-known-good，只记 ERROR；
//   - 严格解析：routes.yaml 未知键报错（DiscardUnknown 的静默丢弃是历史事故源）。
package loader

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/lens077/control-tower/sdk/configsource"
	"github.com/lens077/control-tower/services/gateway/internal/authn"
	"github.com/lens077/control-tower/services/gateway/internal/authz"
	confv1 "github.com/lens077/control-tower/services/gateway/internal/conf/v1"
	"github.com/lens077/control-tower/services/gateway/internal/httpmw"
	"github.com/lens077/control-tower/services/gateway/internal/router"

	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
	"sigs.k8s.io/yaml"
)

// 五个键名。
const (
	KeyRoutes      = "routes.yaml"
	KeyPublicPEM   = "secrets/public.pem"
	KeyPolicies    = "policies/policies.csv"
	KeyModel       = "policies/model.conf"
	KeyRevocations = "auth/revocations.yaml"
)

// State 持有全部可热更新的运行态，并暴露就绪条件。
type State struct {
	verifier *authn.Verifier
	enforcer *authz.Enforcer
	cors     *httpmw.CorsSwapper
	log      *zap.Logger

	table atomic.Pointer[router.Table]

	mu          sync.Mutex // 保护 model/policies 原文配对
	rawModel    []byte
	rawPolicies []byte

	keyReady    atomic.Bool
	revStampNed atomic.Int64 // 最近一次撤销名单应用/心跳的 unix nano
}

// NewState 构造 State。
func NewState(verifier *authn.Verifier, enforcer *authz.Enforcer, cors *httpmw.CorsSwapper, log *zap.Logger) *State {
	return &State{verifier: verifier, enforcer: enforcer, cors: cors, log: log}
}

// Table 返回当前路由表（nil=未加载）。
func (s *State) Table() *router.Table { return s.table.Load() }

// Verifier 返回认证器（装配用）。
func (s *State) Verifier() *authn.Verifier { return s.verifier }

// Enforcer 返回授权判定器（装配用）。
func (s *State) Enforcer() *authz.Enforcer { return s.enforcer }

// Ready 是 readyz 条件：路由表 + 公钥 + Casbin 全部加载成功。
func (s *State) Ready() bool {
	return s.table.Load() != nil && s.keyReady.Load() && s.enforcer.Ready()
}

// RevocationAge 返回撤销名单距上次更新的时长（新鲜度指标；从未加载返回很大值）。
func (s *State) RevocationAge(now time.Time) time.Duration {
	ts := s.revStampNed.Load()
	if ts == 0 {
		return 1<<62 - 1
	}
	return now.Sub(time.Unix(0, ts))
}

// Apply 按键应用一份新数据；错误即保留旧值（调用方只记日志）。
func (s *State) Apply(key string, data []byte) error {
	switch key {
	case KeyRoutes:
		return s.applyRoutes(data)
	case KeyPublicPEM:
		if err := s.verifier.SetPublicKeyPEM(data); err != nil {
			return err
		}
		s.keyReady.Store(true)
		return nil
	case KeyModel:
		return s.applyPolicyPair(data, nil)
	case KeyPolicies:
		return s.applyPolicyPair(nil, data)
	case KeyRevocations:
		return s.applyRevocations(data)
	default:
		return fmt.Errorf("loader: unknown key %q", key)
	}
}

func (s *State) applyRoutes(data []byte) error {
	jsonBytes, err := yaml.YAMLToJSON(data)
	if err != nil {
		return fmt.Errorf("loader: routes yaml: %w", err)
	}
	cfg := &confv1.RouteConfig{}
	// DiscardUnknown 必须为 false：未知键静默丢弃是「配置改了没生效」的历史事故源。
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(jsonBytes, cfg); err != nil {
		return fmt.Errorf("loader: routes decode: %w", err)
	}
	if err := protovalidate.Validate(cfg); err != nil {
		return fmt.Errorf("loader: routes validate: %w", err)
	}
	// 预检 CORS，保证「表 + CORS」两者要么都生效要么都不生效。
	if err := httpmw.ValidateCors(cfg.GetCors()); err != nil {
		return err
	}
	tbl, err := router.Build(cfg)
	if err != nil {
		return err
	}
	s.table.Store(tbl)
	if err := s.cors.Update(cfg.GetCors()); err != nil {
		// 已预检过，不应发生；发生即视为编程错误。
		return err
	}
	return nil
}

func (s *State) applyPolicyPair(model, policies []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if model != nil {
		s.rawModel = model
	}
	if policies != nil {
		s.rawPolicies = policies
	}
	if s.rawModel == nil || s.rawPolicies == nil {
		return nil // 等另一半到位
	}
	return s.enforcer.SetPolicies(string(s.rawModel), string(s.rawPolicies))
}

func (s *State) applyRevocations(data []byte) error {
	table, err := authn.ParseRevocations(data, time.Now())
	if err != nil {
		return err
	}
	s.verifier.SetRevocations(table)
	s.revStampNed.Store(time.Now().UnixNano())
	return nil
}

// requiredBootKeys 是启动必须成功的键（撤销名单允许 NotFound=空表）。
var requiredBootKeys = []string{KeyRoutes, KeyPublicPEM, KeyPolicies, KeyModel}

// RunConfigCenter 以 selector（type=config_center）为基础完成启动加载并常驻 Watch。
// selector 的 key 必须是 routes.yaml；其余键在同 namespace/environment 下派生。
func RunConfigCenter(ctx context.Context, selectorPath string, s *State) error {
	base, err := configsource.LoadSourceConfig(selectorPath)
	if err != nil {
		return err
	}
	if base.Type != configsource.TypeConfigCenter {
		return fmt.Errorf("loader: selector type must be config_center, got %q", base.Type)
	}
	if base.ConfigCenter.Key != KeyRoutes {
		return fmt.Errorf("loader: selector key must be %q, got %q", KeyRoutes, base.ConfigCenter.Key)
	}

	// 启动加载：前四键必须成功。
	for _, key := range requiredBootKeys {
		data, err := configsource.Load(ctx, withKey(base, key))
		if err != nil {
			return fmt.Errorf("loader: initial load %s: %w", key, err)
		}
		if err := s.Apply(key, data); err != nil {
			return fmt.Errorf("loader: initial apply %s: %w", key, err)
		}
	}
	// 撤销名单：NotFound 容忍为空表（键可能尚未创建），其余错误快速失败。
	if data, err := configsource.Load(ctx, withKey(base, KeyRevocations)); err != nil {
		if connect.CodeOf(err) != connect.CodeNotFound {
			return fmt.Errorf("loader: initial load %s: %w", KeyRevocations, err)
		}
		s.verifier.SetRevocations(authn.EmptyRevocations())
		s.revStampNed.Store(time.Now().UnixNano())
		s.log.Warn("revocations key absent; starting with empty revocation table", zap.String("key", KeyRevocations))
	} else if err := s.Apply(KeyRevocations, data); err != nil {
		return fmt.Errorf("loader: initial apply %s: %w", KeyRevocations, err)
	}

	// 常驻 Watch：每键独立循环，断线 1→30s 指数退避（SDK 不内建重连）。
	for _, key := range []string{KeyRoutes, KeyPublicPEM, KeyPolicies, KeyModel, KeyRevocations} {
		go s.watchLoop(ctx, withKey(base, key), key)
	}
	return nil
}

func (s *State) watchLoop(ctx context.Context, cfg configsource.Config, key string) {
	backoff := time.Second
	for {
		err := configsource.Watch(ctx, cfg, func(ev configsource.Event) {
			backoff = time.Second // 有事件即视为连接健康
			switch {
			case ev.Err != nil:
				s.log.Error("watch event error", zap.String("key", key), zap.Error(ev.Err))
			case ev.Deleted:
				// last-known-good：删除不清空运行态（撤销名单也一样——清空会放大授权）。
				s.log.Error("config key deleted; keeping last-known-good", zap.String("key", key))
			default:
				if err := s.Apply(key, ev.Value); err != nil {
					s.log.Error("hot update rejected; keeping last-known-good", zap.String("key", key), zap.Error(err))
					return
				}
				if key == KeyRevocations {
					s.revStampNed.Store(time.Now().UnixNano())
				}
				s.log.Info("hot update applied", zap.String("key", key))
			}
		})
		if ctx.Err() != nil {
			return
		}
		s.log.Warn("watch disconnected; retrying", zap.String("key", key), zap.Error(err), zap.Duration("backoff", backoff))
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func withKey(base configsource.Config, key string) configsource.Config {
	cfg := base
	cfg.ConfigCenter.Key = key
	return cfg
}

// fileNames 是本地目录模式的文件名映射（显式测试/本地模式，不是生产回退）。
var fileNames = map[string]string{
	KeyRoutes:      "routes.yaml",
	KeyPublicPEM:   "public.pem",
	KeyPolicies:    "policies.csv",
	KeyModel:       "model.conf",
	KeyRevocations: "revocations.yaml",
}

// RunFileDir 从本地目录一次性加载五份工件（撤销名单可缺省）。
func RunFileDir(dir string, s *State) error {
	for _, key := range requiredBootKeys {
		data, err := os.ReadFile(filepath.Join(dir, fileNames[key]))
		if err != nil {
			return fmt.Errorf("loader: file mode read %s: %w", key, err)
		}
		if err := s.Apply(key, data); err != nil {
			return fmt.Errorf("loader: file mode apply %s: %w", key, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, fileNames[KeyRevocations]))
	switch {
	case errors.Is(err, os.ErrNotExist):
		s.verifier.SetRevocations(authn.EmptyRevocations())
		s.revStampNed.Store(time.Now().UnixNano())
	case err != nil:
		return err
	default:
		if err := s.Apply(KeyRevocations, data); err != nil {
			return err
		}
	}
	return nil
}
