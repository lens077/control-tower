import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { MetricLineSchema, MetricPointSchema, MetricUnit } from "@/gen/api";
import { buildChartData } from "./metric-series";

function line(label: string, points: [number, number][], unit = MetricUnit.PERCENT) {
  return create(MetricLineSchema, {
    label,
    unit,
    points: points.map(([tsMs, value]) => create(MetricPointSchema, { tsMs: BigInt(tsMs), value })),
  });
}

describe("buildChartData", () => {
  it("没有曲线时返回空结构而不是抛错", () => {
    const result = buildChartData([]);
    expect(result.xAxis).toEqual([]);
    expect(result.series).toEqual([]);
  });

  it("单条曲线按时间升序排列", () => {
    // 后端不保证顺序,这里必须自己排 —— 乱序的点会让折线来回折返
    const { xAxis, series } = buildChartData([
      line("cpu", [
        [3000, 30],
        [1000, 10],
        [2000, 20],
      ]),
    ]);

    expect(xAxis.map((d) => d.getTime())).toEqual([1000, 2000, 3000]);
    expect(series[0].data).toEqual([10, 20, 30]);
  });

  // 这是这个函数存在的理由。P50/P95/P99 来自三次独立查询,
  // 某个时间片只要有一条查不到,长度就不一致 —— 不对齐的话后面的点
  // 会整体错位一格,图照画,但每个点的时间都是错的。
  it("点数不同的曲线按时间戳并集对齐", () => {
    const { xAxis, series } = buildChartData([
      line("P50", [
        [1000, 1],
        [2000, 2],
        [3000, 3],
      ]),
      line("P99", [
        [1000, 9],
        [3000, 27],
      ]), // 中间那个时间片没数据
    ]);

    expect(xAxis.map((d) => d.getTime())).toEqual([1000, 2000, 3000]);
    expect(series[0].data).toEqual([1, 2, 3]);
    expect(series[1].data).toEqual([9, null, 27]);
  });

  it("完全不重叠的时间戳也能对齐", () => {
    const { xAxis, series } = buildChartData([line("a", [[1000, 1]]), line("b", [[2000, 2]])]);

    expect(xAxis).toHaveLength(2);
    expect(series[0].data).toEqual([1, null]);
    expect(series[1].data).toEqual([null, 2]);
  });

  it("缺口填 null 且不连线", () => {
    // 连起来会画出一条并不存在的直线,让人以为那段时间指标平稳
    const { series } = buildChartData([
      line("x", [
        [1000, 1],
        [3000, 3],
      ]),
      line("y", [[2000, 2]]),
    ]);

    expect(series[0].data).toEqual([1, null, 3]);
    expect(series.every((s) => s.connectNulls === false)).toBe(true);
  });

  it("量纲取自第一条曲线", () => {
    // 同一组里的曲线量纲必然相同(由后端的 plan 保证),取第一条即可
    const { unit } = buildChartData([line("rss", [[1000, 1]], MetricUnit.BYTES)]);
    expect(unit).toBe(MetricUnit.BYTES);
  });

  it("图例名原样保留", () => {
    const { series } = buildChartData([line("node2", [[1000, 1]]), line("node3", [[1000, 2]])]);
    expect(series.map((s) => s.label)).toEqual(["node2", "node3"]);
  });
});
