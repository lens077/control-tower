package otel

import (
	"testing"

	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	"github.com/stretchr/testify/require"
)

func TestOptionsFromBootstrapUsesConfigCenterNamespace(t *testing.T) {
	options := optionsFromBootstrap(&confv1.Bootstrap{Observability: &confv1.Observability{
		Enable: true,
		Trace:  &confv1.Observability_Trace{},
		Metric: &confv1.Observability_Metric{},
		Log:    &confv1.Observability_Logging{},
	}})

	require.Equal(t, "config-center", options.ServiceNamespace)
	require.NotNil(t, options.Trace)
	require.NotNil(t, options.Metric)
	require.NotNil(t, options.Logging)
}

func TestOptionsFromBootstrapDisablesAllSignals(t *testing.T) {
	options := optionsFromBootstrap(&confv1.Bootstrap{})
	require.Nil(t, options.Trace)
	require.Nil(t, options.Metric)
	require.Nil(t, options.Logging)
}
