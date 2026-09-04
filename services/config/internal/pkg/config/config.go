package config

import (
	"context"
	"sync"
	"time"

	"github.com/lens077/control-tower/constants"
	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	kitconfig "github.com/lens077/go-connect-kit/config"
	"github.com/lens077/go-connect-kit/env"
	"github.com/spf13/viper"
	"go.uber.org/fx"
)

// Live is the control plane's concrete go-connect-kit configuration value.
type Live = kitconfig.Live[*confv1.Bootstrap]

var (
	confMu   sync.RWMutex
	conf     = &confv1.Bootstrap{}
	presence = defaultPresenceSettings()

	Module = fx.Module("config-adapter",
		fx.Provide(func(lc fx.Lifecycle) (*Live, error) {
			ctx, cancel := context.WithCancel(context.Background())
			lc.Append(fx.Hook{OnStop: func(context.Context) error {
				cancel()
				return nil
			}})

			live, settings, err := load(ctx)
			if err != nil {
				return nil, err
			}
			publish(live.Get(), settings)
			return live, nil
		}),
		fx.Provide(func(live *Live) *confv1.Bootstrap { return live.Get() }),
		fx.Provide(func(_ *confv1.Bootstrap) PresenceSettings { return GetPresenceSettings() }),
	)
)

// PresenceSettings configures optional, ephemeral client-presence state.
type PresenceSettings struct {
	RedisEnabled   bool
	RedisKeyPrefix string
	RedisTTL       time.Duration
}

type snapshotSource struct {
	name string
	raw  map[string]any
}

func (source snapshotSource) Name() string { return source.name }
func (source snapshotSource) Load(context.Context) (map[string]any, error) {
	return source.raw, nil
}

// Init loads the control plane's local self-bootstrap file.
func Init(ctx context.Context) (*confv1.Bootstrap, error) {
	live, settings, err := load(ctx)
	if err != nil {
		return nil, err
	}
	bootstrap := live.Get()
	publish(bootstrap, settings)
	return bootstrap, nil
}

func load(ctx context.Context) (*Live, PresenceSettings, error) {
	path := env.GetEnvString(constants.EnvConfigFile, constants.ConfigFilePath)
	source, err := kitconfig.NewFileSource(path)
	if err != nil {
		return nil, PresenceSettings{}, err
	}
	raw, err := source.Load(ctx)
	if err != nil {
		return nil, PresenceSettings{}, err
	}

	live, err := kitconfig.NewWithOptions[*confv1.Bootstrap](ctx, snapshotSource{
		name: source.Name(),
		raw:  raw,
	}, kitconfig.LoadOptions{
		AllowUnknownFields: true,
		// The self-bootstrap schema predates strict startup validation. Preserve
		// that behavior until its live configuration has been audited.
		SkipValidation: true,
	})
	if err != nil {
		return nil, PresenceSettings{}, err
	}
	return live, presenceFromRaw(raw), nil
}

func presenceFromRaw(raw map[string]any) PresenceSettings {
	v := viper.New()
	_ = v.MergeConfigMap(raw)
	settings := PresenceSettings{
		RedisEnabled:   v.GetBool("data.cache.redis.presence.enabled"),
		RedisKeyPrefix: v.GetString("data.cache.redis.presence.key_prefix"),
		RedisTTL:       v.GetDuration("data.cache.redis.presence.ttl"),
	}
	if settings.RedisKeyPrefix == "" {
		settings.RedisKeyPrefix = "config-center:presence"
	}
	if settings.RedisTTL <= 0 {
		settings.RedisTTL = 90 * time.Second
	}
	return settings
}

func defaultPresenceSettings() PresenceSettings {
	return PresenceSettings{RedisKeyPrefix: "config-center:presence", RedisTTL: 90 * time.Second}
}

func publish(bootstrap *confv1.Bootstrap, settings PresenceSettings) {
	confMu.Lock()
	conf = bootstrap
	presence = settings
	confMu.Unlock()
}

func GetPresenceSettings() PresenceSettings {
	confMu.RLock()
	defer confMu.RUnlock()
	return presence
}

func GetConfig() *confv1.Bootstrap {
	confMu.RLock()
	defer confMu.RUnlock()
	return conf
}
