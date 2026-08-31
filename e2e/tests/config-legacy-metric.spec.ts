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
  const current = await instantQuery(request, "machine_token_legacy_hits");
  expectZeroSeries(current, "machine_token_legacy_hits");

  const window = await instantQuery(request, "max_over_time(machine_token_legacy_hits[7d])");
  expectZeroSeries(window, "machine_token_legacy_hits 七天窗口");
});
