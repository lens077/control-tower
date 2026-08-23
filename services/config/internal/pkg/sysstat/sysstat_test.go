package sysstat

import (
	"os"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/process"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestSampler(t *testing.T) *Sampler {
	t.Helper()
	proc, err := process.NewProcess(int32(os.Getpid()))
	require.NoError(t, err)
	return &Sampler{proc: proc, started: time.Now(), diskPath: "/"}
}

// 第一次采样拿不到速率(没有上一次可差分),但绝不能因此失败或返回 nil ——
// 服务刚起来时控制台就会读到这一份。
func TestSample_首次采样无速率但结构完整(t *testing.T) {
	s := newTestSampler(t)
	snap := s.sample()

	require.NotNil(t, snap)
	assert.Zero(t, snap.CPUPercent, "首次没有上一次可以差分")
	assert.Zero(t, snap.NetRxBytesPerSec)

	// 这些不依赖差分,首次就该有值
	assert.Positive(t, snap.CPULimitCores, "读不到 cgroup 时必须回退到整机核数")
	assert.Positive(t, snap.MemoryLimitBytes)
	assert.Positive(t, snap.MemoryRSSBytes)
	assert.Positive(t, snap.GoHeapInUseBytes)
	assert.Positive(t, snap.Goroutines)
	assert.Positive(t, snap.DiskTotalBytes)
	assert.False(t, snap.SampledAt.IsZero())
}

// 第二次采样才是真正的验证点:速率字段必须落在合法区间,
// 尤其不能出现负数或 NaN —— 那会一路传到前端显示成 "NaN%"。
func TestSample_第二次采样产出合法速率(t *testing.T) {
	s := newTestSampler(t)
	s.sample()
	time.Sleep(60 * time.Millisecond)
	snap := s.sample()

	assert.GreaterOrEqual(t, snap.CPUPercent, 0.0)
	assert.False(t, isNaN(snap.CPUPercent), "CPUPercent 不能是 NaN")
	assert.GreaterOrEqual(t, snap.NetRxBytesPerSec, 0.0)
	assert.GreaterOrEqual(t, snap.NetTxBytesPerSec, 0.0)
	assert.False(t, isNaN(snap.NetRxBytesPerSec))
	assert.Positive(t, snap.Uptime)
}

// 计数器回退(容器重启、网卡重建)时这一轮不出速率,而不是出一个巨大的负值。
func TestSampleNetwork_计数器回退时不产出负速率(t *testing.T) {
	s := newTestSampler(t)
	s.havePrev = true
	// 把上一次的计数设成一个不可能达到的大数,模拟回退
	s.prevRx, s.prevTx = 1<<62, 1<<62

	snap := &Snapshot{}
	s.sampleNetwork(snap, 1.0, true)

	assert.Zero(t, snap.NetRxBytesPerSec)
	assert.Zero(t, snap.NetTxBytesPerSec)
}

// CPU 时间回退同理。真实场景:进程被 checkpoint/restore,或宿主机时钟回拨。
func TestSampleCPU_时间回退时钳到零(t *testing.T) {
	s := newTestSampler(t)
	s.prevCPUSecs = 1e9 // 远大于本进程实际用掉的 CPU 秒数

	snap := &Snapshot{CPULimitCores: 4}
	s.sampleCPU(snap, 1.0, true)

	assert.Zero(t, snap.CPUPercent, "宁可显示 0 也不显示负的百分比")
}

// 单项失败不该拖垮整个快照:把磁盘路径指到一个不存在的地方,
// 磁盘字段为空但 CPU/内存照常有值,并且失败原因进 Degraded。
func TestSample_单项失败只降级不失败(t *testing.T) {
	s := newTestSampler(t)
	s.diskPath = "/definitely/not/a/real/mount/point"

	snap := s.sample()

	assert.Zero(t, snap.DiskTotalBytes)
	assert.Positive(t, snap.MemoryRSSBytes, "磁盘读不到不该影响内存")
	assert.NotEmpty(t, snap.Degraded, "失败原因必须留痕,否则前端只看到 0 会以为是真的 0")
}

func TestSnapshot_并发读安全(t *testing.T) {
	s := newTestSampler(t)
	s.current.Store(s.sample())

	// -race 下跑才有意义:验证读取方不必加锁
	done := make(chan struct{})
	go func() {
		for range 50 {
			s.current.Store(s.sample())
		}
		close(done)
	}()
	for range 50 {
		_ = s.Snapshot()
	}
	<-done
}

func isNaN(f float64) bool { return f != f }
