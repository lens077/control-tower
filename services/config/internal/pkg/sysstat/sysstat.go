// Package sysstat 采样「本进程 / 本 Pod 用了多少资源」。
//
// 为什么不直接查 VictoriaMetrics:VM 里的 system_* 是**整台节点**的,回答不了
// 「配置中心自己吃了多少」。而 otelconnect / otelpgx 白送的那批指标只覆盖
// RPC 与数据库,不含进程资源。两边都缺这一块,所以在进程内自己采。
//
// 采到的值有两个去处:
//   - 经 SystemService 直接返回给控制台,作为「此刻」的读数 —— 没有采集延迟,
//     也不依赖 VM 是否可达;
//   - 经 metrics.go 注册成 OTel 可观测量表推给 VM,给控制台的历史曲线用。
//
// 后台定时采样而不是每次请求现算:CPU 使用率和网络速率都是两次采样的差分,
// 请求触发的话第一次调用必然返回 0,而且两次请求间隔不定会让速率忽高忽低。
package sysstat

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// Interval 是后台采样周期。
//
// 10s 与集群里 otel collector 的 host_metrics collection_interval 对齐,
// 这样控制台上「进程」和「节点」两组数字的时间粒度一致,不会出现一个已经
// 反映了尖峰、另一个还没有的错位。
const Interval = 10 * time.Second

// Snapshot 是一次采样的结果。所有字段都是值,读取方不必加锁。
type Snapshot struct {
	SampledAt time.Time
	Uptime    time.Duration

	// CPUPercent 是本进程占 CPULimitCores 的百分比,0..100。
	// 分母是 cgroup 上限而不是宿主机核数 —— 见 limits 的注释。
	CPUPercent    float64
	CPULimitCores float64

	MemoryRSSBytes   uint64
	MemoryLimitBytes uint64

	// Go 运行时侧。RSS 里含 Go 之外的开销(栈、mmap 的库),两者对不上是正常的,
	// 但 HeapInUse 持续涨而 RSS 不涨基本可以断定是 Go 侧泄漏。
	GoHeapInUseBytes uint64
	Goroutines       int32
	GCCount          uint32

	DiskPath       string
	DiskUsedBytes  uint64
	DiskTotalBytes uint64

	// Pod 网络速率。在 k8s 里进程看到的 netns 就是 Pod 的 netns,
	// 所以这个数正好是 Pod 级流量;在开发机上则是整机流量。
	NetRxBytesPerSec float64
	NetTxBytesPerSec float64

	// LimitsFromCgroup 为 false 表示没读到容器限额,上面两个 Limit 字段
	// 用的是整机规格。前端据此提示「这是宿主机口径」,免得把开发机上
	// 看到的 0.3% 当成生产表现。
	LimitsFromCgroup bool

	// Degraded 记录本次采样中失败的子项。单项失败不让整个快照失败 ——
	// 磁盘读不到不该导致 CPU 也看不见。
	Degraded []string
}

// Sampler 持有跨采样的状态(上一次的 CPU 时间与网络计数),
// 因为速率类指标必须靠差分算。
type Sampler struct {
	logger  *zap.Logger
	proc    *process.Process
	started time.Time

	// 磁盘统计针对哪个路径。容器里 "/" 就是容器根文件系统。
	diskPath string

	current atomic.Pointer[Snapshot]

	// 仅由采样 goroutine 访问,无需加锁
	prevAt      time.Time
	prevCPUSecs float64
	prevRx      uint64
	prevTx      uint64
	havePrev    bool
}

var Module = fx.Module("sysstat",
	fx.Provide(New),
	fx.Invoke(registerMetrics),
)

func New(lc fx.Lifecycle, logger *zap.Logger) (*Sampler, error) {
	proc, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return nil, fmt.Errorf("attach to self process: %w", err)
	}

	s := &Sampler{
		logger:   logger,
		proc:     proc,
		started:  time.Now(),
		diskPath: "/",
	}

	// 先同步采一次:否则服务刚起来时控制台会拿到一个空快照。
	// 这一次拿不到速率(没有上一次可以差分),CPU 与网络会是 0,
	// 下一个周期就正常了。
	s.current.Store(s.sample())

	stop := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go s.loop(stop)
			return nil
		},
		OnStop: func(context.Context) error {
			close(stop)
			return nil
		},
	})

	return s, nil
}

// Snapshot 返回最近一次采样。永不返回 nil。
func (s *Sampler) Snapshot() Snapshot { return *s.current.Load() }

func (s *Sampler) loop(stop <-chan struct{}) {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.current.Store(s.sample())
		}
	}
}

func (s *Sampler) sample() *Snapshot {
	now := time.Now()
	snap := &Snapshot{
		SampledAt: now,
		Uptime:    now.Sub(s.started),
		DiskPath:  s.diskPath,
	}

	elapsed := now.Sub(s.prevAt).Seconds()
	usable := s.havePrev && elapsed > 0

	s.sampleLimits(snap)
	s.sampleCPU(snap, elapsed, usable)
	s.sampleMemory(snap)
	s.sampleRuntime(snap)
	s.sampleDisk(snap)
	s.sampleNetwork(snap, elapsed, usable)

	s.prevAt = now
	s.havePrev = true
	return snap
}

func (s *Sampler) sampleLimits(snap *Snapshot) {
	lim := readCgroupLimits()

	snap.CPULimitCores = lim.cpuCores
	snap.MemoryLimitBytes = lim.memoryBytes
	snap.LimitsFromCgroup = lim.cpuCores > 0 && lim.memoryBytes > 0

	// 回退到整机规格。这条路径在开发机上必走,在没设 limits 的 Pod 里也会走。
	if snap.CPULimitCores == 0 {
		snap.CPULimitCores = float64(runtime.NumCPU())
	}
	if snap.MemoryLimitBytes == 0 {
		if vm, err := mem.VirtualMemory(); err == nil {
			snap.MemoryLimitBytes = vm.Total
		} else {
			snap.Degraded = append(snap.Degraded, "memory_limit: "+err.Error())
		}
	}
}

func (s *Sampler) sampleCPU(snap *Snapshot, elapsed float64, usable bool) {
	times, err := s.proc.Times()
	if err != nil {
		snap.Degraded = append(snap.Degraded, "cpu: "+err.Error())
		return
	}
	// 用户态 + 内核态。不算 iowait 等 —— 那些是等,不是本进程在烧 CPU。
	total := times.User + times.System

	if usable && snap.CPULimitCores > 0 {
		// (这段时间用掉的 CPU 秒数 / 墙上时间) 就是占用的核数,再除以上限。
		cores := (total - s.prevCPUSecs) / elapsed
		snap.CPUPercent = cores / snap.CPULimitCores * 100
		if snap.CPUPercent < 0 {
			// 计数器理论上单调,但容器重启或时钟回拨时可能出负数。
			// 显示 0 好过显示一个负的百分比。
			snap.CPUPercent = 0
		}
	}
	s.prevCPUSecs = total
}

func (s *Sampler) sampleMemory(snap *Snapshot) {
	info, err := s.proc.MemoryInfo()
	if err != nil {
		snap.Degraded = append(snap.Degraded, "memory: "+err.Error())
		return
	}
	snap.MemoryRSSBytes = info.RSS
}

func (s *Sampler) sampleRuntime(snap *Snapshot) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	snap.GoHeapInUseBytes = ms.HeapInuse
	snap.GCCount = ms.NumGC
	snap.Goroutines = int32(runtime.NumGoroutine())
}

func (s *Sampler) sampleDisk(snap *Snapshot) {
	usage, err := disk.Usage(s.diskPath)
	if err != nil {
		snap.Degraded = append(snap.Degraded, "disk: "+err.Error())
		return
	}
	snap.DiskUsedBytes = usage.Used
	snap.DiskTotalBytes = usage.Total
}

func (s *Sampler) sampleNetwork(snap *Snapshot, elapsed float64, usable bool) {
	// pernic=true 才能把 lo 排掉。回环流量在这里是纯噪音:
	// 健康检查、进程内自调用都走 lo,把它算进「网络使用率」会让一个
	// 完全没有外部流量的实例看起来很忙。
	counters, err := net.IOCounters(true)
	if err != nil {
		snap.Degraded = append(snap.Degraded, "network: "+err.Error())
		return
	}

	var rx, tx uint64
	for _, c := range counters {
		if c.Name == "lo" || c.Name == "lo0" {
			continue
		}
		rx += c.BytesRecv
		tx += c.BytesSent
	}

	if usable {
		// 计数器可能因为网卡重建而回退,回退时这一轮不出速率而不是出负数
		if rx >= s.prevRx {
			snap.NetRxBytesPerSec = float64(rx-s.prevRx) / elapsed
		}
		if tx >= s.prevTx {
			snap.NetTxBytesPerSec = float64(tx-s.prevTx) / elapsed
		}
	}
	s.prevRx, s.prevTx = rx, tx
}
