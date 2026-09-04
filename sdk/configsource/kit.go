package configsource

import (
	"context"
	"errors"
	"fmt"
	"time"

	kitconfig "github.com/lens077/go-connect-kit/config"
)

const (
	kitWatchMinBackoff = time.Second
	kitWatchMaxBackoff = 30 * time.Second
)

var _ kitconfig.Source = (*KitSource)(nil)
var _ kitconfig.Watcher = (*KitSource)(nil)

// KitSource adapts the Config Center SDK to go-connect-kit's provider-neutral
// configuration source interface.
type KitSource struct {
	config Config
}

// NewKitSource loads a local Config Center selector and returns a kit source.
func NewKitSource(sourceConfigFile string) (kitconfig.Source, error) {
	cfg, err := LoadSourceConfig(sourceConfigFile)
	if err != nil {
		return nil, err
	}
	if cfg.Type != TypeConfigCenter {
		return nil, fmt.Errorf("%s must select config_center, got %q", sourceConfigFile, cfg.Type)
	}
	return &KitSource{config: cfg}, nil
}

func (s *KitSource) Name() string { return string(s.config.Type) }

func (s *KitSource) Load(ctx context.Context) (map[string]any, error) {
	contents, err := Load(ctx, s.config)
	if err != nil {
		return nil, err
	}
	raw, err := kitconfig.ParseYAML(contents)
	if err != nil {
		return nil, fmt.Errorf("parse Config Center document: %w", err)
	}
	return raw, nil
}

func (s *KitSource) Watch(ctx context.Context, onEvent func(kitconfig.WatchEvent)) error {
	if onEvent == nil {
		return errors.New("config update callback is nil")
	}

	backoff := kitWatchMinBackoff
	for {
		gotEvent := false
		err := Watch(ctx, s.config, func(event Event) {
			gotEvent = true
			switch {
			case event.Err != nil:
				onEvent(kitconfig.WatchEvent{Err: event.Err})
			case event.Deleted:
				onEvent(kitconfig.WatchEvent{Deleted: true})
			default:
				raw, parseErr := kitconfig.ParseYAML(event.Value)
				if parseErr != nil {
					onEvent(kitconfig.WatchEvent{Err: fmt.Errorf("parse Config Center update: %w", parseErr)})
					return
				}
				onEvent(kitconfig.WatchEvent{Raw: raw})
			}
		})
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			err = errors.New("Config Center watch stream ended")
		}
		if gotEvent {
			backoff = kitWatchMinBackoff
		}
		onEvent(kitconfig.WatchEvent{Err: fmt.Errorf("Config Center watch stream ended; retry in %s: %w", backoff, err)})

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > kitWatchMaxBackoff {
			backoff = kitWatchMaxBackoff
		}
	}
}
