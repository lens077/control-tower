package log

import (
	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	kitlog "github.com/lens077/go-connect-kit/log"
	"go.uber.org/fx"
)

// Module projects the control plane's protobuf into go-connect-kit/log.
var Module = kitlog.Module[*confv1.Bootstrap](optionsFromBootstrap)

func FxLogger() fx.Option {
	return kitlog.FxLogger[*confv1.Bootstrap](optionsFromBootstrap)
}

func optionsFromBootstrap(conf *confv1.Bootstrap) kitlog.Options {
	return kitlog.Options{
		Level:               conf.GetLog().GetApplication().GetLevel(),
		Format:              conf.GetLog().GetApplication().GetFormat(),
		FrameworkLogLevel:   conf.GetLog().GetFramework().GetLogLevel(),
		FrameworkErrorLevel: conf.GetLog().GetFramework().GetErrorLevel(),
	}
}
