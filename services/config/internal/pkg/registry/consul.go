package registry

import (
	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	kitregistry "github.com/lens077/go-connect-kit/registry"
	"go.uber.org/fx"
)

// Module projects the control plane's protobuf into go-connect-kit/registry.
var Module = fx.Module("config-registry-adapter",
	fx.Provide(optionsFromBootstrap),
	kitregistry.Module,
)

func optionsFromBootstrap(conf *confv1.Bootstrap) kitregistry.Options {
	consul := conf.GetDiscovery().GetConsul()
	check := consul.GetCheck()
	ttl := check.GetTtl()
	return kitregistry.Options{
		Enabled:       consul != nil && consul.GetAddr() != "",
		Address:       consul.GetAddr(),
		ServerAddress: conf.GetServer().GetAddr(),
		TLS: kitregistry.TLSOptions{
			Enabled:            consul.GetTls().GetEnable(),
			InsecureSkipVerify: consul.GetTls().GetInsecureSkipVerify(),
			CAPEM:              consul.GetTls().GetCaPem(),
		},
		Check: kitregistry.CheckOptions{
			TTL: kitregistry.TTLCheckOptions{
				Enabled:      ttl != nil,
				Duration:     ttl.GetDuration(),
				PingInterval: ttl.GetPingInterval().AsDuration(),
			},
			// The config service is HTTP/Connect. It must not receive a gRPC check.
			GRPC:                           nil,
			DeregisterCriticalServiceAfter: check.GetDeregisterCriticalServiceAfter(),
		},
	}
}
