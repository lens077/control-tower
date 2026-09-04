package otel

import (
	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	kitotel "github.com/lens077/go-connect-kit/otel"
	"go.uber.org/fx"
)

// Module projects the control plane's protobuf into go-connect-kit/otel.
var Module = fx.Module("config-otel-adapter",
	fx.Provide(optionsFromBootstrap),
	kitotel.Module,
)

func optionsFromBootstrap(conf *confv1.Bootstrap) kitotel.Options {
	observability := conf.GetObservability()
	if !observability.GetEnable() {
		return kitotel.Options{}
	}
	return kitotel.Options{
		Trace: &kitotel.TraceOptions{
			Endpoint: observability.GetTrace().GetEndpoint(),
			TLS:      tlsOptions(observability.GetTrace().GetTls()),
		},
		Metric: &kitotel.MetricOptions{
			Endpoint: observability.GetMetric().GetEndpoint(),
			TLS:      tlsOptions(observability.GetMetric().GetTls()),
		},
		Logging: &kitotel.LoggingOptions{
			Endpoint: observability.GetLog().GetEndpoint(),
			TLS:      tlsOptions(observability.GetLog().GetTls()),
		},
		ServiceNamespace: "config-center",
	}
}

func tlsOptions(conf *confv1.Observability_Tls) kitotel.TLSOptions {
	return kitotel.TLSOptions{
		Enabled:            conf.GetEnable(),
		InsecureSkipVerify: conf.GetInsecureSkipVerify(),
		CAPEM:              conf.GetCaPem(),
	}
}
