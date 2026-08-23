import type { MetricLine } from "@/gen/api";
import { MetricUnit } from "@/gen/api";

export interface ChartSeries {
  label: string;
  data: (number | null)[];
  showMark: false;
  connectNulls: false;
  curve: "linear";
}

export interface ChartData {
  xAxis: Date[];
  series: ChartSeries[];
  unit: MetricUnit;
}

/**
 * 把后端的「每条线各自带时间戳」转成图表要的「共享 x 轴 + 对齐的 y 数组」。
 *
 * 必须按时间戳并集对齐,不能假设各条线的点数相同:P50 和 P99 来自三次独立
 * 查询,某个时间片只要有一条查不到数据,长度就会不一致。直接把数组塞进去
 * 会让后面的点整体错位一格 —— 图还是画得出来,但每个点的时间都是错的,
 * 而且看不出来。
 *
 * 对不上的位置填 null。MUI 会把线断开(connectNulls: false),
 * 这正是「那一刻没数据」的正确表达 —— 连起来会画出一条并不存在的直线,
 * 让人以为那段时间指标平稳。
 */
export function buildChartData(lines: MetricLine[]): ChartData {
  if (lines.length === 0) {
    return { xAxis: [], series: [], unit: MetricUnit.UNSPECIFIED };
  }

  const timestamps = new Set<number>();
  for (const line of lines) {
    for (const point of line.points) timestamps.add(Number(point.tsMs));
  }
  const sorted = [...timestamps].sort((a, b) => a - b);
  const indexOf = new Map(sorted.map((ts, index) => [ts, index]));

  const series = lines.map((line): ChartSeries => {
    const data: (number | null)[] = Array.from({ length: sorted.length }, () => null);
    for (const point of line.points) {
      const index = indexOf.get(Number(point.tsMs));
      if (index !== undefined) data[index] = point.value;
    }
    return {
      label: line.label,
      data,
      showMark: false,
      connectNulls: false,
      curve: "linear",
    };
  });

  return { xAxis: sorted.map((ts) => new Date(ts)), series, unit: lines[0].unit };
}
