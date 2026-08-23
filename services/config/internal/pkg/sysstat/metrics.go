package sysstat

import (
	"context"
	"fmt"

	confv1 "github.com/lens077/control-tower/services/config/internal/conf/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

// scopeName 决定 VM 里的 scope_name 标签,用它能一眼分清哪些序列出自这里,
// 哪些来自 otelconnect / otelpgx。
const scopeName = "github.com/lens077/control-tower/services/config/internal/pkg/sysstat"

// registerMetrics 把 Sampler 的快照注册成 OTel 可观测量表(observable gauge)。
//
// 为什么用 observable 而不是每次采样主动 Record:量表是「当前值」语义,
// 由 SDK 在导出周期回调读取即可。主动写的话采样周期(10s)和导出周期
// 若不相等就会出现重复上报或漏报。
//
// 与 SystemService 的关系:同一份快照,两个出口。RPC 那条给「此刻」的数字,
// 这条给 VM 做历史曲线。没有第二次采样,不会出现两处数字对不上。
func registerMetrics(sampler *Sampler, cfg *confv1.Observability, logger *zap.Logger) error {
	if cfg == nil || !cfg.Enable {
		// 可观测性关掉时不注册。控制台的「此刻」读数不受影响 ——
		// 它走 RPC 直接读 Sampler,与 OTel 无关。
		logger.Info("observability disabled, skip sysstat metrics")
		return nil
	}

	meter := otel.Meter(scopeName)

	// 单位遵循 OTel 约定:比率用 "1",字节用 "By"。VM 侧的
	// opentelemetry.usePrometheusNaming 会据此加上 _ratio / _bytes 后缀。
	cpu, err := meter.Float64ObservableGauge("process.cpu.utilization",
		metric.WithDescription("本进程占 CPU 限额的比率,0..1"),
		metric.WithUnit("1"))
	if err != nil {
		return fmt.Errorf("create process.cpu.utilization gauge: %w", err)
	}
	memUsed, err := meter.Int64ObservableGauge("process.memory.usage",
		metric.WithDescription("本进程常驻内存"),
		metric.WithUnit("By"))
	if err != nil {
		return fmt.Errorf("create process.memory.usage gauge: %w", err)
	}
	memLimit, err := meter.Int64ObservableGauge("process.memory.limit",
		metric.WithDescription("本进程内存上限(cgroup 限额,读不到则为整机内存)"),
		metric.WithUnit("By"))
	if err != nil {
		return fmt.Errorf("create process.memory.limit gauge: %w", err)
	}
	heap, err := meter.Int64ObservableGauge("process.runtime.go.heap.usage",
		metric.WithDescription("Go 堆上正在使用的字节数"),
		metric.WithUnit("By"))
	if err != nil {
		return fmt.Errorf("create heap gauge: %w", err)
	}
	goroutines, err := meter.Int64ObservableGauge("process.runtime.go.goroutines",
		metric.WithDescription("goroutine 数量"),
		// 花括号是 OTel 的「注解单位」写法,转 Prometheus 命名时会被整个丢掉,
		// 得到 process_runtime_go_goroutines。这里不能写 "1" —— 那会被当成
		// 比率而加上 _ratio 后缀,一个计数指标叫 ..._ratio 会误导所有读它的人。
		metric.WithUnit("{goroutine}"))
	if err != nil {
		return fmt.Errorf("create goroutines gauge: %w", err)
	}
	netRx, err := meter.Float64ObservableGauge("process.network.io.receive.rate",
		metric.WithDescription("Pod 入向速率"),
		metric.WithUnit("By/s"))
	if err != nil {
		return fmt.Errorf("create net rx gauge: %w", err)
	}
	netTx, err := meter.Float64ObservableGauge("process.network.io.transmit.rate",
		metric.WithDescription("Pod 出向速率"),
		metric.WithUnit("By/s"))
	if err != nil {
		return fmt.Errorf("create net tx gauge: %w", err)
	}

	// 一个回调覆盖全部量表:保证同一个导出周期里的所有值来自同一次采样,
	// 分成多个回调的话可能跨采样,heap 和 goroutines 会对不上。
	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		s := sampler.Snapshot()
		o.ObserveFloat64(cpu, s.CPUPercent/100)
		o.ObserveInt64(memUsed, int64(s.MemoryRSSBytes))
		o.ObserveInt64(memLimit, int64(s.MemoryLimitBytes))
		o.ObserveInt64(heap, int64(s.GoHeapInUseBytes))
		o.ObserveInt64(goroutines, int64(s.Goroutines))
		o.ObserveFloat64(netRx, s.NetRxBytesPerSec)
		o.ObserveFloat64(netTx, s.NetTxBytesPerSec)
		return nil
	}, cpu, memUsed, memLimit, heap, goroutines, netRx, netTx)
	if err != nil {
		return fmt.Errorf("register sysstat callback: %w", err)
	}

	// 刻意不挂任何属性。进程身份已经在 resource 上(service_name /
	// service_version / deployment_environment_name),再加实例维度的属性
	// 只会让多副本时序列翻倍,而「哪个副本」这个问题应该去看日志。
	logger.Info("sysstat metrics registered", zap.String("scope", scopeName))
	return nil
}
