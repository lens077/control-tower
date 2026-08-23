package sysstat

import (
	"os"
	"strconv"
	"strings"
)

// cgroup v2 的统一挂载点。v1 的多层级布局这里不支持 —— 现在的集群
// (containerd + systemd cgroup driver)是 v2,而开发机是 macOS 根本没有 cgroup,
// 为一个既不在生产也不在开发路径上的组合写兼容代码不划算。
// 读不到就回退到整机规格,见 hostFallback。
const cgroupRoot = "/sys/fs/cgroup"

// limits 是「这个进程最多能用多少」。
//
// 为什么必须有:容器里 runtime.NumCPU() 和 /proc/meminfo 报的是宿主机的规格,
// 不是 Pod 的 limits。用宿主机核数当分母算出来的 CPU 使用率,在一个
// limits.cpu=200m 的 Pod 上会永远显示个位数,哪怕它已经被限流到卡死。
type limits struct {
	// CPU 核数上限。0 表示没读到(未设 limit,或不在容器里)
	cpuCores float64
	// 内存字节上限。0 表示没读到
	memoryBytes uint64
}

// readCgroupLimits 读 cgroup v2 的 cpu.max / memory.max。
// 任何一项读不到就留零值,由调用方回退到整机规格。
func readCgroupLimits() limits {
	return limits{
		cpuCores:    readCPUMax(cgroupRoot + "/cpu.max"),
		memoryBytes: readMemoryMax(cgroupRoot + "/memory.max"),
	}
}

// readCPUMax 解析 cpu.max。格式是 "<quota> <period>",两个数都是微秒;
// quota 为字面量 "max" 表示不限制。
//
// 例:"200000 100000" = 每 100ms 最多用 200ms CPU 时间 = 2 核。
// 注意这对应 k8s 的 limits.cpu 而不是 requests.cpu —— requests 落在
// cpu.weight 上,只影响争抢时的份额,不构成上限。
func readCPUMax(path string) float64 {
	fields := strings.Fields(readTrimmed(path))
	if len(fields) != 2 || fields[0] == "max" {
		return 0
	}
	quota, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	period, err := strconv.ParseFloat(fields[1], 64)
	if err != nil || period == 0 {
		return 0
	}
	return quota / period
}

// readMemoryMax 解析 memory.max。要么是字节数,要么是字面量 "max"。
func readMemoryMax(path string) uint64 {
	raw := readTrimmed(path)
	if raw == "" || raw == "max" {
		return 0
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func readTrimmed(path string) string {
	contents, err := os.ReadFile(path)
	if err != nil {
		// 不记日志:macOS 上这必然失败,而且每次采样都会走到这里,
		// 记了就是每几秒一条噪音。回退路径本身是正常行为,不是异常。
		return ""
	}
	return strings.TrimSpace(string(contents))
}
