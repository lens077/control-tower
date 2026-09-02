package schema

import (
	"strings"
	"testing"

	"github.com/lens077/control-tower/services/config/internal/biz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

var requiredSections = map[string][]string{
	"address":   {"server", "data", "auth", "log"},
	"behavior":  {"server", "data", "auth", "recommend", "log"},
	"cart":      {"server", "data", "auth", "log"},
	"inventory": {"server", "data", "auth", "log"},
	"merchant":  {"server", "data", "auth", "log"},
	"order":     {"server", "data", "auth", "log"},
	"payment":   {"server", "data", "auth", "log", "pay"},
	"product":   {"server", "data", "auth", "log"},
	"search":    {"server", "data", "auth", "log"},
	"user":      {"server", "data", "auth", "log"},
}

func minimalBootstrap(namespace string) string {
	var builder strings.Builder
	for _, section := range requiredSections[namespace] {
		builder.WriteString(section)
		builder.WriteString(": {}\n")
	}
	if namespace == "search" {
		builder.WriteString("search:\n  catalog:\n    endpoint: http://127.0.0.1:9200\n    index: ecommerce_catalog_products\n")
	}
	return builder.String()
}

func newTestRegistry(t *testing.T, mode string, logger *zap.Logger) *Registry {
	t.Helper()
	registry, err := newRegistry(bundledSchemas, mode, logger)
	require.NoError(t, err)
	return registry
}

func target(namespace string) biz.ContentTarget {
	return biz.ContentTarget{Namespace: namespace, Environment: "dev", Key: "bootstrap.yaml"}
}

func TestRegistryAcceptsEveryBundledBootstrapSchema(t *testing.T) {
	registry := newTestRegistry(t, modeEnforce, zap.NewNop())
	require.Len(t, registry.schemas, len(requiredSections))

	for namespace := range requiredSections {
		t.Run(namespace, func(t *testing.T) {
			err := registry.Validate(target(namespace), biz.FormatYAML, minimalBootstrap(namespace))
			require.NoError(t, err)
		})
	}
}

func TestRegistryKeepsSearchServiceScoped(t *testing.T) {
	registry := newTestRegistry(t, modeEnforce, zap.NewNop())

	cart := minimalBootstrap("cart") + "search: {}\n"
	err := registry.Validate(target("cart"), biz.FormatYAML, cart)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/")

	require.NoError(t, registry.Validate(target("search"), biz.FormatYAML, minimalBootstrap("search")))
}

func TestRegistryAcceptsGoDurationStrings(t *testing.T) {
	registry := newTestRegistry(t, modeEnforce, zap.NewNop())
	value := minimalBootstrap("cart") + "observability:\n  metric:\n    export_interval: 5s\n"
	require.NoError(t, registry.Validate(target("cart"), biz.FormatYAML, value))
}

func TestRegistryIgnoresUnregisteredTargets(t *testing.T) {
	registry := newTestRegistry(t, modeEnforce, zap.NewNop())
	invalid := "not: [valid for a bootstrap]\n"
	targets := []biz.ContentTarget{
		{Namespace: "future-service", Environment: "dev", Key: "bootstrap.yaml"},
		{Namespace: "cart", Environment: "dev", Key: "feature-flags.yaml"},
		{Namespace: "Cart", Environment: "dev", Key: "bootstrap.yaml"},
		{Namespace: "cart", Environment: "dev", Key: "Bootstrap.yaml"},
		{Namespace: "cart", Environment: "dev", Key: "bootstrap.yml"},
	}
	for _, unregistered := range targets {
		require.NoError(t, registry.Validate(unregistered, biz.FormatYAML, invalid))
	}
}

func TestRegistryRejectsEmptyAndPlaintextRegisteredBootstrap(t *testing.T) {
	registry := newTestRegistry(t, modeEnforce, zap.NewNop())
	cases := []struct {
		name   string
		format biz.ConfigFormat
		value  string
	}{
		{name: "empty", format: biz.FormatYAML, value: ""},
		{name: "comments only", format: biz.FormatYAML, value: "# no configuration\n"},
		{name: "plaintext", format: biz.FormatPlaintext, value: "server={}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := registry.Validate(target("cart"), tc.format, tc.value)
			require.EqualError(t, err, "invalid configuration at /")
		})
	}
}

func TestRegistryNormalizesJSONAndTOML(t *testing.T) {
	registry := newTestRegistry(t, modeEnforce, zap.NewNop())
	jsonValue := `{"server":{},"data":{},"auth":{},"log":{}}`
	tomlValue := "[server]\n[data]\n[auth]\n[log]\n"

	require.NoError(t, registry.Validate(target("cart"), biz.FormatJSON, jsonValue))
	require.NoError(t, registry.Validate(target("cart"), biz.FormatTOML, tomlValue))
}

func TestRegistryErrorRedactsValues(t *testing.T) {
	registry := newTestRegistry(t, modeEnforce, zap.NewNop())
	value := minimalBootstrap("cart") + "leaked_password: hunter2\n"

	err := registry.Validate(target("cart"), biz.FormatYAML, value)
	require.EqualError(t, err, "invalid configuration at /")
	assert.NotContains(t, err.Error(), "hunter2")
	assert.NotContains(t, err.Error(), "leaked_password")
}

func TestRegistryObserveModeAllowsAndLogsLocationsOnly(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	registry := newTestRegistry(t, modeObserve, zap.New(core))
	value := minimalBootstrap("cart") + "leaked_password: hunter2\n"

	require.NoError(t, registry.Validate(target("cart"), biz.FormatYAML, value))
	require.Len(t, logs.All(), 1)
	entry := logs.All()[0]
	assert.Contains(t, entry.ContextMap(), "locations")
	assert.NotContains(t, entry.ContextMap(), "hunter2")
	assert.NotContains(t, entry.ContextMap(), "leaked_password")
}

func TestNewRegistryRejectsUnknownMode(t *testing.T) {
	_, err := newRegistry(bundledSchemas, "disabled", zap.NewNop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), modeEnvironment)
}
