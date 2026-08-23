import { MetricUnit } from "@/gen/api";

/**
 * 按量纲格式化指标值。
 *
 * 之所以由 MetricUnit 驱动而不是按指标名写 switch:后端每加一条曲线,
 * 前端就要跟着加一个分支,而漏加的表现是「字节数被当成个数显示成 3.4e8」——
 * 看起来像个正常数字,没人会觉得不对。
 */
export function formatMetricValue(value: number, unit: MetricUnit): string {
  switch (unit) {
    case MetricUnit.PERCENT:
      return `${value.toFixed(1)}%`;
    case MetricUnit.BYTES:
      return formatBytes(value);
    case MetricUnit.BYTES_PER_SECOND:
      return `${formatBytes(value)}/s`;
    case MetricUnit.MILLISECONDS:
      return formatMilliseconds(value);
    case MetricUnit.REQUESTS_PER_SECOND:
      return `${trimNumber(value)} req/s`;
    default:
      return trimNumber(value);
  }
}

/** 用 1024 进制:这些数字全部来自操作系统(RSS、cgroup 限额、文件系统),
 *  而那边一律是二进制单位。混用 1000 进制会让页面上的「内存 1.0 GB」
 *  与 kubectl top 显示的数字对不上,徒增怀疑。 */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes)) return "-";
  const negative = bytes < 0;
  let value = Math.abs(bytes);
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  let index = 0;
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024;
    index += 1;
  }
  // 数越大小数位越少:1.5 GiB 有意义,1.523456 GiB 只是噪音
  const digits = index === 0 ? 0 : value >= 100 ? 0 : value >= 10 ? 1 : 2;
  return `${negative ? "-" : ""}${value.toFixed(digits)} ${units[index]}`;
}

export function formatMilliseconds(ms: number): string {
  if (!Number.isFinite(ms)) return "-";
  if (ms < 1) return `${(ms * 1000).toFixed(0)} µs`;
  if (ms < 1000) return `${ms.toFixed(ms < 10 ? 2 : 0)} ms`;
  return `${(ms / 1000).toFixed(2)} s`;
}

/** 把 uptime 之类的秒数写成人能读的形式。只保留两个最大的单位 ——
 *  "3d 4h" 比 "3d 4h 12m 7s" 有用得多。 */
export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "-";
  const units: [number, string][] = [
    [86400, "d"],
    [3600, "h"],
    [60, "m"],
    [1, "s"],
  ];
  const parts: string[] = [];
  let rest = Math.floor(seconds);
  for (const [size, suffix] of units) {
    const amount = Math.floor(rest / size);
    if (amount > 0 || parts.length > 0) {
      if (amount > 0) parts.push(`${amount}${suffix}`);
      if (parts.length === 2) break;
    }
    rest %= size;
  }
  return parts.length > 0 ? parts.join(" ") : "0s";
}

/** 去掉无意义的尾随零:12.00 → 12,12.30 → 12.3 */
function trimNumber(value: number): string {
  if (!Number.isFinite(value)) return "-";
  if (Number.isInteger(value)) return String(value);
  return String(Number(value.toFixed(2)));
}

/**
 * 用量的健康色。阈值取 75/90 而不是常见的 80/95:
 * 这是给人看的仪表盘不是告警规则,早一点变黄比晚一点有用。
 */
export function usageSeverity(percent: number): "success" | "warning" | "error" {
  if (percent >= 90) return "error";
  if (percent >= 75) return "warning";
  return "success";
}
