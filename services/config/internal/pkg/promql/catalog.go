package promql

import (
	"fmt"
	"strings"
)

const serviceNamespace = "config-center"

// 本文件是 PromQL 的唯一出处。前端只能按 MetricSeries 枚举点名,
// 拿不到也传不进任何查询语句 —— 见 system.proto 里 QueryMetrics 的注释。
//
// 每条查询下面都记了它依赖哪个采集源。删采集源之前先来这里看一眼,
// 否则表现是「页面上某张图突然空了」,而没有任何报错。

// Query 是目录里的一条查询。
type Query struct {
	// PromQL 模板。%s 会被替换成 service_name 过滤条件(仅进程/API/DB 类需要)。
	Expr string
	// LabelKey 指定用结果里的哪个标签当图例名。空表示用 FixedLabel。
	LabelKey string
	// FixedLabel 用于结果只有一条线、标签里没有合适名字的情况。
	FixedLabel string
}

// Catalog 把一个 MetricSeries 展开成一条或多条查询。
// 分位数那种一次要三条线的,就在这里列三条。
type Catalog struct {
	// 查询里用到的服务名。取自 meta.AppInfo,不是写死的常量 ——
	// 写死的话本仓从 ecommerce 拆出来改了服务名后,页面会静默查不到数据。
	serviceName string
}

func NewCatalog(serviceName string) *Catalog { return &Catalog{serviceName: serviceName} }

// selector 生成配置中心专属的资源过滤条件。service.name 保留 config-service
// 以维持 Consul 和网关发现兼容；service.namespace 区分独立仓的遥测，避免未来
// 同名服务或残留历史序列混入系统页。
// 服务名里出现引号会破坏 PromQL 语法,这里直接剥掉 —— 服务名是内部配置,
// 不是用户输入,出现引号只可能是配错了,剥掉比拼出一句非法查询好排查。
func (c *Catalog) selector() string {
	name := strings.NewReplacer(`"`, "", `\`, "").Replace(c.serviceName)
	return fmt.Sprintf(`service_name="%s",service_namespace="%s"`, name, serviceNamespace)
}

// ProcessCPU 等以下方法各自返回一组查询。命名与 MetricSeries 枚举一一对应。

func (c *Catalog) ProcessCPU() []Query {
	// 来源:internal/pkg/sysstat 注册的 process.cpu.utilization 可观测量表。
	// 量表值是 0..1 的比率,这里乘 100 换成百分比与页面上的即时值口径一致。
	return []Query{{
		Expr:       fmt.Sprintf(`process_cpu_utilization_ratio{%s} * 100`, c.selector()),
		FixedLabel: "cpu",
	}}
}

func (c *Catalog) ProcessMemory() []Query {
	return []Query{
		{Expr: fmt.Sprintf(`process_memory_usage_bytes{%s}`, c.selector()), FixedLabel: "rss"},
		{Expr: fmt.Sprintf(`process_runtime_go_heap_usage_bytes{%s}`, c.selector()), FixedLabel: "go heap"},
		// 上限画成一条水平线,让「离打爆还有多远」一眼可见,
		// 而不是要读者心算 RSS 除以某个记在别处的数字。
		{Expr: fmt.Sprintf(`process_memory_limit_bytes{%s}`, c.selector()), FixedLabel: "limit"},
	}
}

func (c *Catalog) ProcessGoroutines() []Query {
	return []Query{{
		Expr:       fmt.Sprintf(`process_runtime_go_goroutines{%s}`, c.selector()),
		FixedLabel: "goroutines",
	}}
}

func (c *Catalog) ProcessNetwork() []Query {
	return []Query{
		{Expr: fmt.Sprintf(`process_network_io_receive_rate_bytes_per_second{%s}`, c.selector()), FixedLabel: "rx"},
		{Expr: fmt.Sprintf(`process_network_io_transmit_rate_bytes_per_second{%s}`, c.selector()), FixedLabel: "tx"},
	}
}

func (c *Catalog) HostCPU() []Query {
	// 来源:otel collector 的 host_metrics cpu scraper。
	//
	// 必须先按核聚合再对核取平均。直接 sum 会把 4 核机器的满载算成 400%
	// —— 这是最容易写错的一条,实测过:同一时刻 sum 给 116%、avg 给 28%。
	return []Query{{
		Expr: `avg by (k8s_node_name) (` +
			`sum by (k8s_node_name, cpu) (system_cpu_utilization_ratio{state!="idle"})` +
			`) * 100`,
		LabelKey: "k8s_node_name",
	}}
}

func (c *Catalog) HostMemory() []Query {
	return []Query{{
		Expr:     `sum by (k8s_node_name) (system_memory_utilization_ratio{state="used"}) * 100`,
		LabelKey: "k8s_node_name",
	}}
}

func (c *Catalog) HostDisk() []Query {
	// 只看根分区。collector 侧已经排掉了 kubelet 的 PVC 挂载点,
	// 这里再挡一次 /boot 之类 —— 它们容量小、永远不动,画上去只是噪音。
	return []Query{{
		Expr: `sum by (k8s_node_name) (system_filesystem_usage_bytes{mountpoint="/",state="used"}) / ` +
			`sum by (k8s_node_name) (system_filesystem_usage_bytes{mountpoint="/"}) * 100`,
		LabelKey: "k8s_node_name",
	}}
}

func (c *Catalog) HostNetwork() []Query {
	return []Query{{
		Expr:     `sum by (direction) (rate(system_network_io_bytes_total[5m]))`,
		LabelKey: "direction",
	}}
}

func (c *Catalog) APILatency() []Query {
	// 来源:otelconnect 拦截器的 rpc.server.duration 直方图。
	//
	// 不按 rpc_method 拆:一张图上五个方法乘三个分位数是十五条线,没法看。
	// 要看单个方法的分位数应该去 Grafana —— 那里有变量下拉。
	base := fmt.Sprintf(`sum by (le) (rate(rpc_server_duration_milliseconds_bucket{%s}[5m]))`, c.selector())
	return []Query{
		{Expr: fmt.Sprintf(`histogram_quantile(0.50, %s)`, base), FixedLabel: "P50"},
		{Expr: fmt.Sprintf(`histogram_quantile(0.95, %s)`, base), FixedLabel: "P95"},
		{Expr: fmt.Sprintf(`histogram_quantile(0.99, %s)`, base), FixedLabel: "P99"},
	}
}

func (c *Catalog) APIThroughput() []Query {
	// 这条按方法拆是有意义的:方法数量有限(当前 10 个),而且
	// 「哪个接口在被打」正是看吞吐时要回答的问题。
	return []Query{{
		Expr:     fmt.Sprintf(`sum by (rpc_method) (rate(rpc_server_duration_milliseconds_count{%s}[5m]))`, c.selector()),
		LabelKey: "rpc_method",
	}}
}

func (c *Catalog) APIErrorRate() []Query {
	// otelconnect 只在出错时才打 rpc_connect_rpc_error_code 标签,
	// 成功的请求这个标签不存在。所以分子用「标签存在且非空」来选,
	// 分母用全部 —— 不能写 code!="ok",那样会把成功的请求也算进分子。
	//
	// 分子外面那个 `or on() vector(0)` 是必须的:一次错误都没发生时,
	// 分子匹配不到任何序列,是个空集合,除法的结果也就是空 —— 图上一片空白。
	// 而「服务完全健康」恰恰是最常见的状态,一个健康时看起来像坏了的错误率图
	// 毫无用处。补上 0 之后,空白就只剩一个含义:窗口内根本没有请求。
	sel := c.selector()
	return []Query{{
		Expr: fmt.Sprintf(
			`(sum(rate(rpc_server_duration_milliseconds_count{%s,rpc_connect_rpc_error_code!=""}[5m])) or on() vector(0)) / `+
				`sum(rate(rpc_server_duration_milliseconds_count{%s}[5m])) * 100`, sel, sel),
		FixedLabel: "error rate",
	}}
}

func (c *Catalog) DBLatency() []Query {
	// 来源:otelpgx。单位是秒,乘 1000 与 API 延迟统一成毫秒,
	// 免得两张图并排放着却是两个量纲。
	base := fmt.Sprintf(`sum by (le) (rate(db_client_operation_duration_seconds_bucket{%s}[5m]))`, c.selector())
	return []Query{
		{Expr: fmt.Sprintf(`histogram_quantile(0.50, %s) * 1000`, base), FixedLabel: "P50"},
		{Expr: fmt.Sprintf(`histogram_quantile(0.95, %s) * 1000`, base), FixedLabel: "P95"},
		{Expr: fmt.Sprintf(`histogram_quantile(0.99, %s) * 1000`, base), FixedLabel: "P99"},
	}
}

func (c *Catalog) DBPool() []Query {
	// pgxpool 的连接池水位。acquired 逼近 max 就是池要见底了 ——
	// 这时候的表现是请求排队,而 API 延迟图上只看得到「变慢了」,
	// 看不出是慢在数据库还是慢在等连接。
	sel := c.selector()
	return []Query{
		{Expr: fmt.Sprintf(`pgxpool_acquired_connections{%s}`, sel), FixedLabel: "acquired"},
		{Expr: fmt.Sprintf(`pgxpool_idle_connections{%s}`, sel), FixedLabel: "idle"},
		{Expr: fmt.Sprintf(`pgxpool_max_connections{%s}`, sel), FixedLabel: "max"},
	}
}
