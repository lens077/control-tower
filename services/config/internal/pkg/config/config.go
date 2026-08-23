package config

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/lens077/control-tower/constants"
	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	"github.com/lens077/control-tower/services/config/internal/pkg/env"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
	"go.uber.org/fx"
	"google.golang.org/protobuf/types/known/durationpb"
)

var (
	confMu   sync.RWMutex
	conf     = &confv1.Bootstrap{}
	presence = PresenceSettings{RedisKeyPrefix: "config-center:presence", RedisTTL: 90 * time.Second}

	Module = fx.Module("config",
		fx.Provide(func(lc fx.Lifecycle) (*confv1.Bootstrap, error) {
			ctx, cancel := context.WithCancel(context.Background())
			lc.Append(fx.Hook{OnStop: func(context.Context) error {
				cancel()
				return nil
			}})
			return Init(ctx)
		}),
		fx.Provide(func(_ *confv1.Bootstrap) PresenceSettings {
			return GetPresenceSettings()
		}),
	)
)

// PresenceSettings is deliberately separate from the generated service
// bootstrap: it configures optional, ephemeral observability state only.
// Redis remains a required cache dependency, but this feature does not write
// presence data unless explicitly enabled in the local configuration file.
type PresenceSettings struct {
	RedisEnabled   bool
	RedisKeyPrefix string
	RedisTTL       time.Duration
}

func decodeConfig(data map[string]any, target any) error {
	v := viper.New()
	v.SetConfigType(constants.ConfigFileFormat)
	for key, value := range data {
		v.Set(key, value)
	}

	stringToProtoDurationHook := func(from, to reflect.Type, value any) (any, error) {
		if from.Kind() != reflect.String || to != reflect.TypeOf(&durationpb.Duration{}) {
			return value, nil
		}
		duration, err := time.ParseDuration(value.(string))
		if err != nil {
			return nil, err
		}
		return durationpb.New(duration), nil
	}

	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		TagName:    "json",
		DecodeHook: mapstructure.ComposeDecodeHookFunc(stringToProtoDurationHook),
		Result:     target,
	})
	if err != nil {
		return err
	}
	return decoder.Decode(v.AllSettings())
}

// Init loads this control plane's bootstrap configuration from a local file.
// Keeping this bootstrap outside ConfigService prevents a self-hosting cycle.
func Init(context.Context) (*confv1.Bootstrap, error) {
	path := env.GetEnvString(constants.EnvConfigFile, constants.ConfigFilePath)
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read local config %q: %w", path, err)
	}

	v := viper.New()
	v.SetConfigType(constants.ConfigFileFormat)
	if err := v.ReadConfig(bytes.NewReader(contents)); err != nil {
		return nil, fmt.Errorf("parse local config %q: %w", path, err)
	}

	bootstrap := &confv1.Bootstrap{}
	if err := decodeConfig(v.AllSettings(), bootstrap); err != nil {
		return nil, fmt.Errorf("decode local config %q: %w", path, err)
	}

	confMu.Lock()
	conf = bootstrap
	presence = PresenceSettings{
		RedisEnabled:   v.GetBool("data.cache.redis.presence.enabled"),
		RedisKeyPrefix: v.GetString("data.cache.redis.presence.key_prefix"),
		RedisTTL:       v.GetDuration("data.cache.redis.presence.ttl"),
	}
	if presence.RedisKeyPrefix == "" {
		presence.RedisKeyPrefix = "config-center:presence"
	}
	if presence.RedisTTL <= 0 {
		presence.RedisTTL = 90 * time.Second
	}
	confMu.Unlock()
	return bootstrap, nil
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
