package server

import (
	"context"

	"github.com/lens077/control-tower/services/config/internal/data"
	"github.com/lens077/go-connect-kit/meta"
)

type HealthStatus struct {
	Healthy bool `json:"healthy"`
	// Version 是 API 契约版本(形如 v1),Build 是构建制品版本(ldflags 注入)。
	// 两者并列暴露而不合并:合并后就无法区分「v1 契约」和「哪次构建」,
	// 而排障时最常问的恰恰是后者。
	Version string            `json:"version"`
	Build   string            `json:"build"`
	Details map[string]string `json:"details,omitempty"`
}

func healthStatus(ctx context.Context, deps *data.Data, info meta.AppInfo) HealthStatus {
	details := make(map[string]string)
	healthy := true

	// 注册独立的检查项
	checks := map[string]func(context.Context) error{
		"postgres": deps.CheckDatabase,
		"redis":    deps.CheckCache,
	}

	for name, check := range checks {
		state := "ok"
		if err := check(ctx); err != nil {
			state = err.Error()
			healthy = false
		}
		details[name] = state
	}

	return HealthStatus{
		Healthy: healthy,
		Version: info.Version,
		Build:   meta.Version,
		Details: details,
	}
}
