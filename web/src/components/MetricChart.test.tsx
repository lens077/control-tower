import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { create } from "@bufbuild/protobuf";
import { afterEach, beforeAll, describe, expect, test, vi } from "vite-plus/test";
import { MetricChart } from "./MetricChart";
import { MetricLineSchema, MetricUnit, SeriesResultSchema } from "@/gen/api";
import { i18next, initI18n } from "@/i18n";
import configZh from "@/locales/zh-CN/config.json";
import configEn from "@/locales/en/config.json";

vi.mock("@mui/x-charts/LineChart", () => ({
  LineChart: ({ series }: { series: Array<{ label: string }> }) => (
    <div data-testid="chart">{series.map((line) => line.label).join(", ")}</div>
  ),
}));

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
let root: Root | undefined;
let container: HTMLDivElement | undefined;

beforeAll(async () => {
  await initI18n({ ns: "config", resources: { "zh-CN": configZh, en: configEn }, locale: "zh-CN" });
  await i18next.changeLanguage("zh-CN");
});

afterEach(() => {
  act(() => root?.unmount());
  container?.remove();
});

function renderChart(labels: Array<[string, number | undefined]>, error = "") {
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  const result = create(SeriesResultSchema, {
    error,
    lines: labels.map(([label, value]) => create(MetricLineSchema, {
      label,
      unit: MetricUnit.PERCENT,
      points: value === undefined ? [] : [{ tsMs: 1000n, value }],
    })),
  });
  act(() => root!.render(<MetricChart title="主机 CPU" result={result} emptyHint="所选时间窗内无数据" />));
}

describe("MetricChart 数据缺失", () => {
  test("仍有健康曲线时显式列出整窗缺失主机", () => {
    renderChart([["k1", 0], ["node4", undefined]]);
    expect(container?.textContent).toContain("无数据");
    expect(container?.textContent).toContain("node4");
    expect(container?.querySelector('[data-testid="chart"]')?.textContent).toBe("k1");
  });

  test("全为空占位时提示缺失而非绘制空图", () => {
    renderChart([["k1", undefined], ["node4", undefined]]);
    expect(container?.textContent).toContain("所选时间窗内无数据");
    expect(container?.textContent).toContain("node4");
    expect(container?.querySelector('[data-testid="chart"]')).toBeNull();
  });

  test("部分查询失败不会被现存曲线掩盖", () => {
    renderChart([["k1", 12]], "主机记录超过 180s，保留历史数据");
    expect(container?.querySelector('[role="alert"]')?.textContent).toContain("超过 180s");
    expect(container?.querySelector('[data-testid="chart"]')?.textContent).toBe("k1");
  });
});
