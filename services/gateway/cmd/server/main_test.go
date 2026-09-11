package main

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/lens077/control-tower/services/gateway/internal/observability"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
)

func TestRunCleansUpObservabilityWhenWiringFails(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "collector:4318")
	t.Setenv("OTEL_TRACES_SAMPLER_RATIO", "1")
	t.Setenv("JWT_ISSUER", "")
	t.Setenv("CASDOOR_URL", "")
	t.Setenv("JWT_AUDIENCES", "")

	var shutdownCalls atomic.Int32
	setup := func(context.Context, observability.Config, *zap.Logger) (func(context.Context) error, error) {
		return func(context.Context) error {
			shutdownCalls.Add(1)
			return nil
		}, nil
	}

	err := runWithObservability(fxtest.NewLifecycle(t), zap.NewNop(), setup)
	require.ErrorContains(t, err, "JWT_ISSUER/CASDOOR_URL")
	require.EqualValues(t, 1, shutdownCalls.Load())
}
