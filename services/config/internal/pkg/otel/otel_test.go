package otel

import (
	"testing"

	"github.com/lens077/control-tower/services/config/internal/pkg/meta"
	"github.com/stretchr/testify/require"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

func TestNewResource_UsesConfigCenterNamespace(t *testing.T) {
	res, err := newResource(meta.AppInfo{Name: "config-service"})
	require.NoError(t, err)

	value, ok := res.Set().Value(semconv.ServiceNamespaceKey)
	require.True(t, ok)
	require.Equal(t, "config-center", value.AsString())
}
