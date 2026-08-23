package log

import (
	"os"

	"github.com/lens077/control-tower/constants"
	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	"github.com/lens077/control-tower/services/config/internal/pkg/meta"
	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel/log/global"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap/zapcore"

	"go.uber.org/fx"
	"go.uber.org/zap"
)

var Module = fx.Module("log",
	fx.Provide(
		func(conf *confv1.Bootstrap, info meta.AppInfo) *zap.Logger {
			return NewLogger(conf, info)
		},
	),
)

func FxLogger() fx.Option {
	return fx.WithLogger(func(log *zap.Logger, conf *confv1.Bootstrap) fxevent.Logger {
		zlog := &fxevent.ZapLogger{Logger: log}

		var fxLogLevel zapcore.Level
		if err := fxLogLevel.UnmarshalText([]byte(conf.Log.Framework.LogLevel)); err != nil {
			fxLogLevel = zapcore.DebugLevel
		}
		var fxErrLevel zapcore.Level
		if err := fxErrLevel.UnmarshalText([]byte(conf.Log.Framework.ErrorLevel)); err != nil {
			fxErrLevel = zapcore.ErrorLevel
		}

		zlog.UseLogLevel(fxLogLevel)
		zlog.UseErrorLevel(fxErrLevel)

		return zlog
	})
}

func NewLogger(conf *confv1.Bootstrap, info meta.AppInfo) *zap.Logger {
	logConfig := conf.Log.Application
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(logConfig.Level)); err != nil {
		level = zapcore.DebugLevel
	}

	var encoder zapcore.Encoder
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	if logConfig.Format == constants.FormatConsole {
		encoderConfig = zap.NewDevelopmentEncoderConfig()
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	}

	stdCore := zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), level)

	otelCore := otelzap.NewCore(
		info.Name,
		otelzap.WithLoggerProvider(global.GetLoggerProvider()),
	)

	core := zapcore.NewTee(stdCore, otelCore)

	return zap.New(core, zap.AddCaller())
}
