import { act, type ComponentType } from "react";
import { createRoot, type Root } from "react-dom/client";
import { notifyManager, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vite-plus/test";
import { create } from "@bufbuild/protobuf";
import { Route } from "./system";
import { systemApi } from "@/api";
import { GetSystemStatusResponseSchema, MetricSeries, MetricUnit, QueryMetricsResponseSchema } from "@/gen/api";
import { i18next, initI18n } from "@/i18n";
import configZh from "@/locales/zh-CN/config.json";
import configEn from "@/locales/en/config.json";

vi.mock("@/api", async () => ({
  MetricSeries: (await import("@/gen/api")).MetricSeries,
  TIME_RANGES: { "1h": { windowSeconds: 3600, stepSeconds: 30 } },
  systemApi: { getSystemStatus: vi.fn(), queryMetrics: vi.fn() },
}));
vi.mock("@/api/transport", () => ({
  toAppError: (error: unknown) => ({ message: error instanceof Error ? error.message : "Request failed" }),
}));

vi.mock("@/components/MetricChart", () => ({
  MetricChart: ({ title, result }: { title: string; result?: { error: string; lines: Array<{ label: string }> } }) => (
    <section aria-label={title}>{result?.error || result?.lines.map((line) => line.label).join(", ")}</section>
  ),
}));

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
let root: Root | undefined;
let container: HTMLDivElement | undefined;
let queryClient: QueryClient | undefined;

beforeAll(async () => {
  notifyManager.setScheduler(queueMicrotask);
  await initI18n({ ns: "config", resources: { "zh-CN": configZh, en: configEn }, locale: "zh-CN" });
  await i18next.changeLanguage("zh-CN");
});

afterAll(() => notifyManager.setScheduler((callback) => setTimeout(callback, 0)));

afterEach(async () => {
  await act(async () => root?.unmount());
  queryClient?.clear();
  container?.remove();
  vi.clearAllMocks();
});

async function renderPage() {
  container = document.createElement("div");
  document.body.append(container);
  root = createRoot(container);
  queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  vi.mocked(systemApi.getSystemStatus).mockResolvedValue(create(GetSystemStatusResponseSchema));
  const SystemPage = Route.options.component as ComponentType & { preload?: () => Promise<unknown> };
  // 路由插件会自动拆包;先完成真实模块加载,不把首次 Suspense 的时序当成业务错误。
  await SystemPage.preload?.();
  await act(async () => root!.render(<QueryClientProvider client={queryClient!}><SystemPage /></QueryClientProvider>));
}

describe("System 页主机请求状态", () => {
  test("指标请求失败显式提示,不伪装为无数据", async () => {
    vi.mocked(systemApi.queryMetrics).mockRejectedValue(new Error("VM query unavailable"));
    await renderPage();
    await vi.waitFor(() => expect(container?.textContent).toContain("VM query unavailable"));
  });

  test("重查失败不继续把上次成功数据当成当前结果", async () => {
    const success = create(QueryMetricsResponseSchema, {
      metricsBackendAvailable: true,
      results: [{
        series: MetricSeries.HOST_MEMORY,
        lines: [{ label: "stale-node", unit: MetricUnit.PERCENT, points: [{ tsMs: 1000n, value: 20 }] }],
      }],
    });
    vi.mocked(systemApi.queryMetrics).mockImplementation(async (requested) => create(QueryMetricsResponseSchema, {
      metricsBackendAvailable: true,
      results: success.results.filter((result) => requested.includes(result.series)),
    }));
    await renderPage();
    await vi.waitFor(() => expect(container?.textContent).toContain("stale-node"));
    vi.mocked(systemApi.queryMetrics).mockRejectedValue(new Error("VM request failed"));
    await act(async () => { await queryClient!.refetchQueries({ queryKey: ["systemMetricsInfra"] }); });
    await vi.waitFor(() => expect(container?.textContent).toContain("VM request failed"));
    expect(container?.textContent).not.toContain("stale-node");
  });

  test("两个批次中任意明确未配置后端都显示不可用", async () => {
    vi.mocked(systemApi.queryMetrics).mockImplementation(async (series) => create(QueryMetricsResponseSchema, {
      metricsBackendAvailable: !series.includes(MetricSeries.HOST_MEMORY),
    }));
    await renderPage();
    await vi.waitFor(() => expect(container?.textContent).toContain("未配置指标查询端"));
  });
});
