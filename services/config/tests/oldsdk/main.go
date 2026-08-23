// 跨版本探针：旧 SDK v0.1.0 → 新 config 服务。
// 用法见 README.md；由 scripts/crossversion.sh 编排。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/lens077/config-center/sdk/configsource"
)

var (
	mode      = flag.String("mode", "load", "load | watch")
	address   = flag.String("addr", "http://127.0.0.1:18095", "config 服务地址")
	namespace = flag.String("namespace", "order", "namespace")
	env       = flag.String("env", "dev", "environment")
	key       = flag.String("key", "bootstrap.yaml", "key")
	token     = flag.String("token", "", "service token")
	timeout   = flag.Duration("timeout", 90*time.Second, "整体超时")
)

func main() {
	flag.Parse()
	cfg := configsource.Config{
		Type: configsource.TypeConfigCenter,
		ConfigCenter: configsource.ConfigCenterConfig{
			Address:      *address,
			Namespace:    *namespace,
			Environment:  *env,
			Key:          *key,
			ServiceToken: *token,
			ClientName:   "oldsdk-probe",
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	switch *mode {
	case "load":
		data, err := configsource.Load(ctx, cfg)
		if err != nil {
			fmt.Printf("LOAD_ERR %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("LOAD_OK bytes=%d\n", len(data))
	case "watch":
		start := time.Now()
		err := configsource.Watch(ctx, cfg, func(ev configsource.Event) {
			switch {
			case ev.Err != nil:
				fmt.Printf("EVENT_ERR t=%.1fs %v\n", time.Since(start).Seconds(), ev.Err)
			case ev.Deleted:
				fmt.Printf("EVENT_DELETED t=%.1fs\n", time.Since(start).Seconds())
			default:
				fmt.Printf("EVENT_VALUE t=%.1fs bytes=%d\n", time.Since(start).Seconds(), len(ev.Value))
			}
		})
		// Watch 返回即流结束：吊销断流场景在这里量时延。
		fmt.Printf("STREAM_END t=%.1fs err=%v\n", time.Since(start).Seconds(), err)
		if err != nil {
			os.Exit(2)
		}
	default:
		fmt.Println("unknown mode")
		os.Exit(64)
	}
}
