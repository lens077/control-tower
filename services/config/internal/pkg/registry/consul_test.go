package registry

import (
	"testing"
	"time"

	"github.com/lens077/control-tower/constants"
	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestOptionsFromBootstrapOmitsGRPCReadiness(t *testing.T) {
	t.Setenv(constants.EnvConsulEnabled, "")
	options := optionsFromBootstrap(&confv1.Bootstrap{
		Server: &confv1.Server{Addr: "0.0.0.0:30010"},
		Discovery: &confv1.Discovery{Consul: &confv1.Discovery_Consul{
			Addr: "consul:8500",
			Check: &confv1.Discovery_Consul_Check{
				Ttl: &confv1.Discovery_Consul_Check_TTL{
					Duration:     "30s",
					PingInterval: durationpb.New(10 * time.Second),
				},
				DeregisterCriticalServiceAfter: "1m",
			},
		}},
	})

	require.True(t, options.Enabled)
	require.Equal(t, "0.0.0.0:30010", options.ServerAddress)
	require.True(t, options.Check.TTL.Enabled)
	require.Nil(t, options.Check.GRPC, "the HTTP/Connect config service must not get a gRPC readiness check")
}

func TestOptionsFromBootstrapPreservesConsulEnvironmentOverride(t *testing.T) {
	t.Setenv(constants.EnvConsulEnabled, "false")
	options := optionsFromBootstrap(&confv1.Bootstrap{
		Discovery: &confv1.Discovery{Consul: &confv1.Discovery_Consul{Addr: "consul:8500"}},
	})
	require.False(t, options.Enabled)
}
