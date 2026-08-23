package configsource

import (
	"os"
	"path/filepath"
	"testing"

	configv1 "github.com/lens077/control-tower/api/config/v1"
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

func TestConfigCenterRequest_AddsClientIdentityHeaders(t *testing.T) {
	request := configCenterRequest(&configv1.GetKeyRequest{}, ConfigCenterConfig{
		ServiceToken:   "reader-token",
		ClientName:     "cart-service",
		ClientInstance: "cart-7d8f",
		ClientVersion:  "dev",
	})

	assert.Equal(t, "reader-token", request.Header().Get("x-config-center-service-token"))
	assert.Equal(t, "cart-service", request.Header().Get("x-config-center-client-name"))
	assert.Equal(t, "cart-7d8f", request.Header().Get("x-config-center-client-instance"))
	assert.Equal(t, "dev", request.Header().Get("x-config-center-client-version"))
}

func TestLoadSourceConfig_RejectsIncompleteSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.yaml")
	require.NoError(t, os.WriteFile(path, []byte("type: config_center\nconfig_center:\n  address: http://config-service:30010\n"), 0o600))

	_, err := LoadSourceConfig(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "namespace")
}
