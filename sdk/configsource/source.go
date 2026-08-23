// Package configsource loads a service Bootstrap document from a selected
// local file or ConfigService endpoint.
package configsource

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"connectrpc.com/connect"
	configv1 "github.com/lens077/control-tower/api/config/v1"
	"github.com/lens077/control-tower/api/config/v1/configv1connect"
	"gopkg.in/yaml.v3"
)

type Type string

const (
	TypeFile         Type = "file"
	TypeConfigCenter Type = "config_center"
)

var ErrUnsupportedWatch = errors.New("configuration source does not support watch")

// Config is the Go representation of api/sdk/v1/source.proto. Applications
// decode this small local bootstrap file before loading their full Bootstrap.
type Config struct {
	Type         Type               `yaml:"type" json:"type"`
	File         FileConfig         `yaml:"file" json:"file"`
	ConfigCenter ConfigCenterConfig `yaml:"config_center" json:"config_center"`
}

type FileConfig struct {
	Path string `yaml:"path" json:"path"`
}

type ConfigCenterConfig struct {
	Address        string `yaml:"address" json:"address"`
	Namespace      string `yaml:"namespace" json:"namespace"`
	Environment    string `yaml:"environment" json:"environment"`
	Key            string `yaml:"key" json:"key"`
	ServiceToken   string `yaml:"service_token" json:"service_token"`
	ClientName     string `yaml:"client_name" json:"client_name"`
	ClientInstance string `yaml:"client_instance" json:"client_instance"`
	ClientVersion  string `yaml:"client_version" json:"client_version"`
}

type Event struct {
	Value   []byte
	Deleted bool
	Err     error
}

// LoadSourceConfig decodes the small, local source selector before a service
// requests its full Bootstrap. Keeping it separate from Load prevents a
// Config Center bootstrap cycle.
func LoadSourceConfig(path string) (Config, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read source config %q: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(contents, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse source config %q: %w", path, err)
	}
	if err := validate(cfg); err != nil {
		return Config{}, fmt.Errorf("validate source config %q: %w", path, err)
	}
	return cfg, nil
}

// Load returns the complete configuration document selected by Config.
func Load(ctx context.Context, cfg Config) ([]byte, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}
	switch cfg.Type {
	case TypeFile:
		contents, err := os.ReadFile(cfg.File.Path)
		if err != nil {
			return nil, fmt.Errorf("read config file %q: %w", cfg.File.Path, err)
		}
		if len(contents) == 0 {
			return nil, fmt.Errorf("config file %q is empty", cfg.File.Path)
		}
		return contents, nil
	case TypeConfigCenter:
		return loadConfigCenter(ctx, cfg.ConfigCenter)
	default:
		return nil, fmt.Errorf("unsupported configuration source %q", cfg.Type)
	}
}

func validate(cfg Config) error {
	switch cfg.Type {
	case TypeFile:
		if cfg.File.Path == "" {
			return errors.New("file.path is required")
		}
	case TypeConfigCenter:
		if cfg.ConfigCenter.Address == "" || cfg.ConfigCenter.Namespace == "" || cfg.ConfigCenter.Environment == "" || cfg.ConfigCenter.Key == "" {
			return errors.New("config_center.address, namespace, environment, and key are required")
		}
	default:
		return fmt.Errorf("unsupported configuration source %q", cfg.Type)
	}
	return nil
}

func loadConfigCenter(ctx context.Context, cfg ConfigCenterConfig) ([]byte, error) {
	client := configv1connect.NewConfigServiceClient(http.DefaultClient, cfg.Address)
	response, err := client.GetKey(ctx, configCenterRequest(&configv1.GetKeyRequest{
		Namespace: cfg.Namespace, Environment: cfg.Environment, Key: cfg.Key,
	}, cfg))
	if err != nil {
		return nil, fmt.Errorf("read config center key %s/%s/%s: %w", cfg.Namespace, cfg.Environment, cfg.Key, err)
	}
	value := response.Msg.GetEntry().GetValue()
	if value == "" {
		return nil, fmt.Errorf("config center key %s/%s/%s is empty", cfg.Namespace, cfg.Environment, cfg.Key)
	}
	return []byte(value), nil
}

// Watch streams ConfigService changes. File is intentionally startup-only;
// callers must restart to pick up file changes.
func Watch(ctx context.Context, cfg Config, onEvent func(Event)) error {
	if cfg.Type != TypeConfigCenter {
		return ErrUnsupportedWatch
	}
	if err := validate(cfg); err != nil {
		return err
	}
	client := configv1connect.NewConfigServiceClient(http.DefaultClient, cfg.ConfigCenter.Address)
	stream, err := client.WatchKeys(ctx, configCenterRequest(&configv1.WatchKeysRequest{
		Namespace: cfg.ConfigCenter.Namespace, Environment: cfg.ConfigCenter.Environment, Keys: []string{cfg.ConfigCenter.Key},
	}, cfg.ConfigCenter))
	if err != nil {
		return fmt.Errorf("watch config center key: %w", err)
	}
	defer func() { _ = stream.Close() }()

	for stream.Receive() {
		message := stream.Msg()
		switch message.GetType() {
		case configv1.WatchEventType_WATCH_EVENT_TYPE_HEARTBEAT:
			continue
		case configv1.WatchEventType_WATCH_EVENT_TYPE_DELETE:
			onEvent(Event{Deleted: true})
		default:
			entry := message.GetEntry()
			if entry == nil || entry.GetValue() == "" {
				onEvent(Event{Err: fmt.Errorf("config center key %s/%s/%s has an empty update",
					cfg.ConfigCenter.Namespace, cfg.ConfigCenter.Environment, cfg.ConfigCenter.Key)})
				continue
			}
			onEvent(Event{Value: []byte(entry.GetValue())})
		}
	}
	return stream.Err()
}

func configCenterRequest[T any](message *T, cfg ConfigCenterConfig) *connect.Request[T] {
	request := connect.NewRequest(message)
	if cfg.ServiceToken != "" {
		request.Header().Set("x-config-center-service-token", cfg.ServiceToken)
	}
	setClientHeaders(request, cfg)
	return request
}

func setClientHeaders[T any](request *connect.Request[T], cfg ConfigCenterConfig) {
	name := cfg.ClientName
	if name == "" {
		name = os.Getenv("SERVICE_NAME")
	}
	instance := cfg.ClientInstance
	if instance == "" {
		instance = os.Getenv("HOSTNAME")
	}
	version := cfg.ClientVersion
	if version == "" {
		version = os.Getenv("SERVICE_VERSION")
	}
	if name != "" {
		request.Header().Set("x-config-center-client-name", name)
	}
	if instance != "" {
		request.Header().Set("x-config-center-client-instance", instance)
	}
	if version != "" {
		request.Header().Set("x-config-center-client-version", version)
	}
}
