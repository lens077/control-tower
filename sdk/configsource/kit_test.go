package configsource

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"connectrpc.com/connect"
	configv1 "github.com/lens077/control-tower/api/config/v1"
	"github.com/lens077/control-tower/api/config/v1/configv1connect"
	kitconfig "github.com/lens077/go-connect-kit/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewKitSourceLoadsConfigCenterSelector(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.yaml")
	contents := "type: config_center\nconfig_center:\n  address: http://config-center:30010\n  namespace: ecommerce\n  environment: dev\n  key: bootstrap.yaml\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	source, err := NewKitSource(path)
	if err != nil {
		t.Fatalf("NewKitSource() error = %v", err)
	}
	if source.Name() != "config_center" {
		t.Fatalf("source name = %q", source.Name())
	}
}

type kitTestConfigService struct {
	configv1connect.UnimplementedConfigServiceHandler
}

func (kitTestConfigService) GetKey(context.Context, *connect.Request[configv1.GetKeyRequest]) (*connect.Response[configv1.GetKeyResponse], error) {
	return connect.NewResponse(&configv1.GetKeyResponse{Entry: &configv1.ConfigEntry{
		Value: "server:\n  addr: 0.0.0.0:30006\n",
	}}), nil
}

func (kitTestConfigService) WatchKeys(ctx context.Context, _ *connect.Request[configv1.WatchKeysRequest], stream *connect.ServerStream[configv1.WatchKeysResponse]) error {
	if err := stream.Send(&configv1.WatchKeysResponse{
		Type:  configv1.WatchEventType_WATCH_EVENT_TYPE_SNAPSHOT,
		Entry: &configv1.ConfigEntry{Value: "server:\n  addr: 0.0.0.0:30007\n"},
	}); err != nil {
		return err
	}
	if err := stream.Send(&configv1.WatchKeysResponse{Type: configv1.WatchEventType_WATCH_EVENT_TYPE_DELETE}); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestKitSourceLoadsAndMapsWatchEvents(t *testing.T) {
	path, handler := configv1connect.NewConfigServiceHandler(kitTestConfigService{})
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	selector := filepath.Join(t.TempDir(), "source.yaml")
	contents := "type: config_center\nconfig_center:\n  address: " + server.URL + "\n  namespace: ecommerce\n  environment: dev\n  key: cart/bootstrap.yaml\n"
	require.NoError(t, os.WriteFile(selector, []byte(contents), 0o600))
	source, err := NewKitSource(selector)
	require.NoError(t, err)

	raw, err := source.Load(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "0.0.0.0:30006", raw["server"].(map[string]any)["addr"])

	watcher, ok := source.(kitconfig.Watcher)
	require.True(t, ok)
	ctx, cancel := context.WithCancel(context.Background())
	var events []kitconfig.WatchEvent
	require.NoError(t, watcher.Watch(ctx, func(event kitconfig.WatchEvent) {
		events = append(events, event)
		if event.Deleted {
			cancel()
		}
	}))
	require.Len(t, events, 2)
	assert.Equal(t, "0.0.0.0:30007", events[0].Raw["server"].(map[string]any)["addr"])
	assert.True(t, events[1].Deleted)
}

func TestNewKitSourceRejectsFileSelector(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.yaml")
	configPath := filepath.Join(t.TempDir(), "bootstrap.yaml")
	contents := "type: file\nfile:\n  path: " + configPath + "\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := NewKitSource(path); err == nil {
		t.Fatal("NewKitSource() error = nil")
	}
}
