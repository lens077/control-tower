package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lens077/control-tower/constants"
	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testBootstrapYAML = `
server:
  addr: "0.0.0.0:30010"
  http:
    read_timeout: 10s
    write_timeout: 20s
    idle_timeout: 1m30s
data:
  database:
    postgres:
      host: localhost
      port: 5432
      user: postgres
      db_name: config
  cache:
    redis:
      host: localhost
discovery:
  consul:
    addr: 127.0.0.1:8500
    scheme: http
`

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func TestDecodeConfig(t *testing.T) {
	raw := map[string]any{"server": map[string]any{"addr": "0.0.0.0:30010"}}
	got := &confv1.Bootstrap{}
	require.NoError(t, decodeConfig(raw, got))
	assert.Equal(t, "0.0.0.0:30010", got.GetServer().GetAddr())
}

func TestInitReadsLocalFile(t *testing.T) {
	t.Setenv(constants.EnvConfigFile, writeConfig(t, testBootstrapYAML))
	got, err := Init(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "0.0.0.0:30010", got.GetServer().GetAddr())
	assert.Equal(t, 10*time.Second, got.GetServer().GetHttp().GetReadTimeout().AsDuration())
	assert.Same(t, got, GetConfig())
}

func TestInitReadsOptionalRedisPresenceSettings(t *testing.T) {
	contents := strings.Replace(testBootstrapYAML, `    redis:
      host: localhost`, `    redis:
      presence:
        enabled: true
        key_prefix: "gray:presence"
        ttl: 2m
      host: localhost`, 1)
	t.Setenv(constants.EnvConfigFile, writeConfig(t, contents))
	_, err := Init(context.Background())
	require.NoError(t, err)
	settings := GetPresenceSettings()
	assert.True(t, settings.RedisEnabled)
	assert.Equal(t, "gray:presence", settings.RedisKeyPrefix)
	assert.Equal(t, 2*time.Minute, settings.RedisTTL)
}

func TestInitRejectsInvalidLocalYAML(t *testing.T) {
	t.Setenv(constants.EnvConfigFile, writeConfig(t, "server:\n\taddr: invalid"))
	got, err := Init(context.Background())
	assert.Nil(t, got)
	require.Error(t, err)
}

func TestInitReportsMissingLocalFile(t *testing.T) {
	t.Setenv(constants.EnvConfigFile, filepath.Join(t.TempDir(), "missing.yaml"))
	got, err := Init(context.Background())
	assert.Nil(t, got)
	require.Error(t, err)
}

func TestGetConfigConcurrentWithInit(t *testing.T) {
	t.Setenv(constants.EnvConfigFile, writeConfig(t, testBootstrapYAML))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { assert.NotNil(t, GetConfig()) })
	}
	for range 4 {
		wg.Go(func() { _, _ = Init(context.Background()) })
	}
	wg.Wait()
}

func TestModule(t *testing.T) {
	assert.Contains(t, Module.String(), "config")
}
