package resolver

import (
	"context"
	"strconv"
	"time"

	capi "github.com/hashicorp/consul/api"
)

// ConsulLister 以 Consul 健康目录实现 Lister（blocking query，只取 passing）。
//
// 注意：ACL 缺 token 时 Consul 读接口可能 200 返回空列表而不报错（历史坑）；
// 空结果的告警与 last-known-good 兜底由 Watching 层统一处理。
type ConsulLister struct {
	client *capi.Client
	// waitTime 是 blocking query 的最长挂起时长。
	waitTime time.Duration
}

// NewConsulLister 构造 ConsulLister。client 由装配层用 CONSUL_ADDR/CONSUL_HTTP_TOKEN
// 等标准环境变量构造（HashiCorp client 自行读取 CONSUL_HTTP_TOKEN，勿再造 CONSUL_TOKEN 常量——历史坑）。
func NewConsulLister(client *capi.Client) *ConsulLister {
	return &ConsulLister{client: client, waitTime: 30 * time.Second}
}

// List 实现 Lister。
func (c *ConsulLister) List(ctx context.Context, service string, index uint64) ([]Instance, uint64, error) {
	entries, meta, err := c.client.Health().Service(service, "", true, (&capi.QueryOptions{
		WaitIndex: index,
		WaitTime:  c.waitTime,
	}).WithContext(ctx))
	if err != nil {
		return nil, index, err
	}
	out := make([]Instance, 0, len(entries))
	for _, e := range entries {
		addr := e.Service.Address
		if addr == "" {
			addr = e.Node.Address
		}
		weight := 0
		if w, ok := e.Service.Meta["weight"]; ok {
			if n, perr := strconv.Atoi(w); perr == nil {
				weight = n
			}
		}
		out = append(out, Instance{
			Addr:   addr + ":" + strconv.Itoa(e.Service.Port),
			Weight: weight,
		})
	}
	return out, meta.LastIndex, nil
}
