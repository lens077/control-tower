package observability

import (
	"context"
	"testing"
	"time"
)

// 带 endpoint 的完整构建路径必须能成功（semconv 与 SDK resource.Default() 的
// schema 版本冲突会在这里暴露——该冲突曾在集群启动时炸出 conflicting Schema URL）。
func TestSetupWithEndpointBuilds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdown, err := Setup(ctx, Config{
		ServiceName:    "t",
		ServiceVersion: "test",
		Environment:    "test",
		Endpoint:       "127.0.0.1:1", // 不会真正连接；导出在后台才发生
		Insecure:       true,
		SampleRatio:    0.5,
	})
	if err != nil {
		t.Fatalf("Setup must build with endpoint: %v", err)
	}
	sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer scancel()
	_ = shutdown(sctx) // 导出端不可达的错误可忽略，只验构建/关停不 panic
}

func TestSetupNoEndpointNoop(t *testing.T) {
	shutdown, err := Setup(context.Background(), Config{})
	if err != nil || shutdown == nil {
		t.Fatalf("no-op path: err=%v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
