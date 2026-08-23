import { createClient } from "@connectrpc/connect";
import { SystemService, MetricSeries, MetricUnit } from "@/gen/api";
import { createAppTransport } from "@/api/transport";

const transport = createAppTransport();
const client = createClient(SystemService, transport);

/**
 * 历史曲线的时间窗。step 与窗口成套定义,而不是让调用方自由组合 ——
 * 后端对「点数」有上限(2000),窗口和步长搭配不当会直接被拒。
 *
 * 最短窗口按 30s 导出周期查询;比它更细只会拿到插值出来的假点。
 */
export const TIME_RANGES = {
  "15m": { windowSeconds: 15 * 60, stepSeconds: 30 },
  "1h": { windowSeconds: 60 * 60, stepSeconds: 30 },
  "6h": { windowSeconds: 6 * 60 * 60, stepSeconds: 120 },
  "24h": { windowSeconds: 24 * 60 * 60, stepSeconds: 300 },
} as const;

export type TimeRangeKey = keyof typeof TIME_RANGES;

export const systemApi = {
  getSystemStatus: (signal?: AbortSignal) => client.getSystemStatus({}, { signal }),

  queryMetrics: (series: MetricSeries[], range: TimeRangeKey, signal?: AbortSignal) => {
    const { windowSeconds, stepSeconds } = TIME_RANGES[range];
    return client.queryMetrics(
      {
        series,
        // Duration 的 seconds 是 bigint(proto 的 int64),传 number 会在
        // 序列化时抛 TypeError,而错误信息只说「不是 bigint」,不说是哪个字段
        window: { seconds: BigInt(windowSeconds), nanos: 0 },
        stepSeconds,
      },
      { signal },
    );
  },
};

export { MetricSeries, MetricUnit };
