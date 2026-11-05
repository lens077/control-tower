package configsource

import (
	"connectrpc.com/connect/v2"
	"context"

	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadSourceConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.yaml")
	require.NoError(t, os.WriteFile(path, []byte("type: file\nfile:\n  path: /etc/cart.yaml\n"), 0o600))

	cfg, err := LoadSourceConfig(path)
	require.NoError(t, err)
	assert.Equal(t, TypeFile, cfg.Type)
	assert.Equal(t, "/etc/cart.yaml", cfg.File.Path)
}

func TestConfigCenterContext_AddsClientIdentityHeaders(t *testing.T) {
	ctx := configCenterContext(context.Background(), ConfigCenterConfig{
		ServiceToken:   "reader-token",
		ClientName:     "cart-service",
		ClientInstance: "cart-7d8f",
		ClientVersion:  "dev",
	})

	// v2 的请求头在客户端 CallInfo 里,不再挂在消息上。
	info, ok := connect.CallInfoForClientContext(ctx)
	require.True(t, ok)
	assert.Equal(t, "reader-token", info.RequestHeader().Get("x-config-center-service-token"))
	assert.Equal(t, "cart-service", info.RequestHeader().Get("x-config-center-client-name"))
	assert.Equal(t, "cart-7d8f", info.RequestHeader().Get("x-config-center-client-instance"))
	assert.Equal(t, "dev", info.RequestHeader().Get("x-config-center-client-version"))
}

func TestLoadSourceConfig_RejectsIncompleteSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.yaml")
	require.NoError(t, os.WriteFile(path, []byte("type: config_center\nconfig_center:\n  address: http://config-service:30010\n"), 0o600))

	_, err := LoadSourceConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace")
}
