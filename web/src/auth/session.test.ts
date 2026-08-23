import { afterEach, beforeEach, describe, expect, test, vi } from "vite-plus/test";
import { clearTokens, getAccessToken, setTokens } from "@/auth";
import { silentRenew } from "@/auth/pkce";
import { renewSession, restoreSession, scheduleRenew, stopRenew } from "@/auth/session";

vi.mock("@/auth/pkce", () => ({
  refreshTokens: vi.fn(),
  silentRenew: vi.fn(),
}));

const silentRenewMock = vi.mocked(silentRenew);

describe("会话续期", () => {
  beforeEach(() => {
    stopRenew();
    clearTokens();
    vi.clearAllMocks();
  });

  afterEach(() => {
    stopRenew();
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  test("登出会使进行中的续期结果失效", async () => {
    let resolveRenew!: (value: { accessToken: string; expiresAt: number }) => void;
    silentRenewMock.mockReturnValue(
      new Promise((resolve) => {
        resolveRenew = resolve;
      }),
    );

    const renewal = renewSession();
    stopRenew();
    resolveRenew({ accessToken: "renewed-access-token", expiresAt: Date.now() + 60_000 });

    await expect(renewal).rejects.toThrow("会话续期已取消");
    expect(getAccessToken()).toBeNull();
  });

  test("已取消的旧恢复请求不会清除更新后的会话", async () => {
    let resolveRenew!: (value: { accessToken: string; expiresAt: number }) => void;
    silentRenewMock.mockReturnValue(
      new Promise((resolve) => {
        resolveRenew = resolve;
      }),
    );

    const restoration = restoreSession();
    stopRenew();
    const payload = btoa(JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 60 }))
      .replace(/\+/g, "-")
      .replace(/\//g, "_")
      .replace(/=+$/, "");
    setTokens({ accessToken: `e30.${payload}.signature` });
    resolveRenew({ accessToken: "stale-access-token", expiresAt: Date.now() + 60_000 });

    await expect(restoration).resolves.toBe(true);
    expect(getAccessToken()).toContain(payload);
  });

  test("定时续期失败会在令牌过期后清理认证状态", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-01T00:00:00Z"));
    silentRenewMock.mockRejectedValue(new Error("identity provider unavailable"));
    const warning = vi.spyOn(console, "warn").mockImplementation(() => {});
    setTokens({ accessToken: "expiring-access-token", expiresAt: Date.now() + 16_000 });

    scheduleRenew();
    await vi.advanceTimersByTimeAsync(16_100);

    expect(getAccessToken()).toBeNull();
    expect(silentRenewMock).toHaveBeenCalledTimes(1);
    expect(warning).toHaveBeenCalled();
  });
});
