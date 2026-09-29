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

func TestInitPreservesPermissiveSelfBootstrap(t *testing.T) {
	t.Setenv(constants.EnvConfigFile, writeConfig(t, "future_section:\n  enabled: true\n"))
	got, err := Init(context.Background())
	require.NoError(t, err)
	assert.Nil(t, got.GetServer(), "control-tower self-bootstrap intentionally skips required-field validation")
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

// 自举配置允许未知字段(AllowUnknownFields),字段名写错会被静默丢弃。
// dev-local.sh 渲染的 metric_query.bearer_token 必须真的落到结构体里,
// 否则公网 metrics.apikv.com(vmauth 只读)会一直 401,System 页面静默变成空图。
func TestInitReadsMetricQueryBearerToken(t *testing.T) {
	contents := testBootstrapYAML + `observability:
  enable: false
  metric_query:
    endpoint: 'https://metrics.apikv.com'
    timeout: 5s
    bearer_token: 'read-token-123'
    tls:
      enable: true
`
	t.Setenv(constants.EnvConfigFile, writeConfig(t, contents))
	got, err := Init(context.Background())
	require.NoError(t, err)
	query := got.GetObservability().GetMetricQuery()
	assert.Equal(t, "https://metrics.apikv.com", query.GetEndpoint())
	assert.Equal(t, "read-token-123", query.GetBearerToken())
}
