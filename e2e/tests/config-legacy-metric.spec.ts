import { expect, test, type APIRequestContext } from "@playwright/test";
import { METRICS_URL } from "../playwright.config";

interface InstantQueryResponse {
  status: string;
  data?: {
    resultType?: string;
    result?: Array<{
      metric?: Record<string, string>;
      value?: [number, string];
    }>;
  };
}

async function instantQuery(request: APIRequestContext, query: string): Promise<InstantQueryResponse> {
  const response = await request.get(`${METRICS_URL}/api/v1/query`, { params: { query } });
  expect(response.ok(), await response.text()).toBe(true);
  return response.json() as Promise<InstantQueryResponse>;
}

function expectZeroSeries(payload: InstantQueryResponse, label: string) {
  expect(payload.status).toBe("success");
  expect(payload.data?.resultType).toBe("vector");
  const result = payload.data?.result ?? [];
  expect(result.length, `${label} 没有时序；不能把「指标未接线」当作零命中`).toBeGreaterThan(0);
  for (const series of result) {
    expect(Number(series.value?.[1]), `${label} 非零：${JSON.stringify(series.metric ?? {})}`).toBe(0);
  }
}

test("legacy 共享 token 当前值与七天窗口保持零命中", async ({ request }) => {
  // 活着的实例当前累计值必须是 0：任何一次 legacy 回退都会把它顶起来，
  // 而且只要那个 Pod 还在，这条断言就一直红。
  const current = await instantQuery(request, "machine_token_legacy_hits");
  expectZeroSeries(current, "machine_token_legacy_hits");

  // ⚠️ 窗口断言必须用 increase 而不是 max_over_time。
  // `machine_token_legacy_hits` 是**累计计数器**，max_over_time 取的是窗口内的最大累计值——
  // 它回答的是「这个实例历史上有没有命中过」，而不是「窗口内有没有新命中」。
  // 两者在 Pod 重启时分叉：旧实例带着历史总数变成一条 stale 时序，只要它最后一个样本
  // 还没滑出 7d，断言就一直红，与窗口内的真实行为无关。
  // 2026-09-20 实测踩到过：pre 旧实例 0db7e519 带着 12 停在 2026-09-15T10:00Z（切 TCR
  // 镜像那次滚动前的 Pod），此后所有实例当前值都是 0，但 max_over_time 仍报 12。
  // increase 会处理计数器重置，且只看窗口内的增量，才是「零命中窗口」要问的问题。
  const window = await instantQuery(request, "increase(machine_token_legacy_hits[7d])");
  expectZeroSeries(window, "machine_token_legacy_hits 七天窗口增量");
});
