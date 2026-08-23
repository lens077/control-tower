package sysstat

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// write 把内容写进临时文件并返回路径。用真文件而不是把解析逻辑抽成
// 接受 string 的函数:读不到文件、文件为空这两条分支同样要覆盖,
// 而它们只有走真实的 os.ReadFile 才测得到。
func write(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "value")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	return path
}

func TestReadCPUMax(t *testing.T) {
	cases := map[string]struct {
		contents string
		want     float64
	}{
		// k8s 的 limits.cpu 换算过来就长这样:每 period 微秒里最多用 quota 微秒
		"两核":       {"200000 100000\n", 2},
		"半核":       {"50000 100000\n", 0.5},
		"200m":     {"20000 100000", 0.2},
		"未设上限":     {"max 100000\n", 0},
		"字段数不对":    {"100000", 0},
		"quota非数字": {"abc 100000", 0},
		"period为零": {"100000 0", 0}, // 除零保护,不能 panic 也不能出 +Inf
		"空文件":      {"", 0},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.want, readCPUMax(write(t, c.contents)))
		})
	}
}

func TestReadCPUMax_文件不存在(t *testing.T) {
	// macOS 开发机上这是常态路径,必须安静地返回 0 而不是报错
	assert.Zero(t, readCPUMax(filepath.Join(t.TempDir(), "nope")))
}

func TestReadMemoryMax(t *testing.T) {
	cases := map[string]struct {
		contents string
		want     uint64
	}{
		"128Mi": {"134217728\n", 134217728},
		"未设上限":  {"max\n", 0},
		"非数字":   {"lots", 0},
		"空文件":   {"", 0},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, c.want, readMemoryMax(write(t, c.contents)))
		})
	}
}

func TestReadMemoryMax_文件不存在(t *testing.T) {
	assert.Zero(t, readMemoryMax(filepath.Join(t.TempDir(), "nope")))
}
