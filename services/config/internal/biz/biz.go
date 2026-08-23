package biz

import "go.uber.org/fx"

var Module = fx.Module("biz",
	fx.Provide(
		NewConfigUseCase,
		NewMachineTokenUseCase,
	),
)
