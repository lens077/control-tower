import { afterAll, beforeAll, beforeEach, describe, expect, test, vi } from "vite-plus/test";
import {
  buildLoginUrl,
  exchangeCode,
  isSilentTransaction,
  refreshTokens,
  silentRenew,
} from "@/auth/pkce";
import { loadRuntimeConfig } from "@/runtime-config";

const originalFetch = globalThis.fetch;

beforeAll(async () => {
  globalThis.fetch = vi.fn().mockResolvedValue(
    new Response(
      JSON.stringify({
        apiUrl: "http://localhost:30010",
        casdoor: {
          serverUrl: "https://identity.example.com",
          clientId: "config-center-web",
          organizationName: "lens",
          appName: "config-center",
          redirectPath: "/callback",
        },
      }),
      { status: 200, headers: { "Content-Type": "application/json" } },
    ),
  );
  await loadRuntimeConfig();
});

afterAll(() => {
  globalThis.fetch = originalFetch;
});

beforeEach(() => {
  sessionStorage.clear();
});

describe("Casdoor PKCE", () => {
  test("授权请求使用随机 state 与 S256 challenge，不包含 client_secret", async () => {
    const url = new URL(await buildLoginUrl());

    expect(url.origin).toBe("https://identity.example.com");
    expect(url.pathname).toBe("/login/oauth/authorize");
    expect(url.searchParams.get("state")).toBeTruthy();
    expect(url.searchParams.get("code_challenge")).toBeTruthy();
    expect(url.searchParams.get("code_challenge_method")).toBe("S256");
    expect(url.searchParams.has("client_secret")).toBe(false);
  });

  test("回调校验 state，并直接向 Casdoor 发送 code_verifier", async () => {
    const loginUrl = new URL(await buildLoginUrl());
    const state = loginUrl.searchParams.get("state");
    expect(state).toBeTruthy();

    const tokenFetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          access_token: "access-token",
          refresh_token: "refresh-token",
          id_token: "id-token",
          expires_in: 600,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    globalThis.fetch = tokenFetch;

    const result = await exchangeCode("authorization-code", state!);
    expect(result.accessToken).toBe("access-token");
    expect(tokenFetch).toHaveBeenCalledOnce();

    const [requestUrl, init] = tokenFetch.mock.calls[0] as [string, RequestInit];
    expect(requestUrl).toBe("https://identity.example.com/api/login/oauth/access_token");
    const body = init.body as URLSearchParams;
    expect(body.get("code")).toBe("authorization-code");
    expect(body.get("code_verifier")).toBeTruthy();
    expect(body.has("client_secret")).toBe(false);
  });

  test("并发授权按 state 隔离，不会互相覆盖 verifier", async () => {
    const interactive = new URL(await buildLoginUrl());
    const silent = new URL(await buildLoginUrl(true));
    const interactiveState = interactive.searchParams.get("state")!;
    const silentState = silent.searchParams.get("state")!;

    expect(interactiveState).not.toBe(silentState);
    expect(isSilentTransaction(interactiveState)).toBe(false);
    expect(isSilentTransaction(silentState)).toBe(true);

    const tokenFetch = vi.fn().mockImplementation(() =>
      Promise.resolve(
        new Response(JSON.stringify({ access_token: "access-token", expires_in: "600" }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );
    globalThis.fetch = tokenFetch;

    const result = await exchangeCode("interactive-code", interactiveState);
    expect(result.refreshToken).toBeNull();
    expect(result.idToken).toBeNull();
    expect(result.expiresAt).toBeGreaterThan(Date.now() + 590_000);

    await exchangeCode("silent-code", silentState);
    expect(tokenFetch).toHaveBeenCalledTimes(2);
  });

  test("静默续期同时绑定 iframe 来源与自身 state", async () => {
    const interactiveState = new URL(await buildLoginUrl()).searchParams.get("state")!;
    const tokenFetch = vi.fn().mockImplementation(() =>
      Promise.resolve(
        new Response(JSON.stringify({ access_token: "silent-access-token", expires_in: 600 }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );
    globalThis.fetch = tokenFetch;

    const renewal = silentRenew(1000);
    await vi.waitFor(() => expect(document.querySelector("iframe")).not.toBeNull());
    const iframe = document.querySelector("iframe") as HTMLIFrameElement;
    const state = new URL(iframe.src).searchParams.get("state")!;
    const message = { type: "config_center_oauth_silent_result", code: "silent-code", state };

    window.dispatchEvent(
      new MessageEvent("message", {
        origin: window.location.origin,
        source: window,
        data: message,
      }),
    );
    window.dispatchEvent(
      new MessageEvent("message", {
        origin: window.location.origin,
        source: iframe.contentWindow,
        data: { ...message, state: interactiveState },
      }),
    );
    expect(tokenFetch).not.toHaveBeenCalled();

    window.dispatchEvent(
      new MessageEvent("message", {
        origin: window.location.origin,
        source: iframe.contentWindow,
        data: message,
      }),
    );
    await expect(renewal).resolves.toMatchObject({ accessToken: "silent-access-token" });
    await expect(exchangeCode("interactive-code", interactiveState)).resolves.toBeDefined();
    expect(tokenFetch).toHaveBeenCalledTimes(2);
    expect(document.querySelector("iframe")).toBeNull();
  });

  test("refresh 响应省略轮换令牌时显式保留现有值", async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ access_token: "new-access-token", expires_in: 600 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const result = await refreshTokens("current-refresh-token");
    expect(result.refreshToken).toBeUndefined();
    expect(result.idToken).toBeUndefined();
  });

  test("state 不匹配时在发网络请求前拒绝", async () => {
    await buildLoginUrl();
    const tokenFetch = vi.fn();
    globalThis.fetch = tokenFetch;

    await expect(exchangeCode("authorization-code", "attacker-state")).rejects.toThrow(
      "OAuth state 校验失败",
    );
    expect(tokenFetch).not.toHaveBeenCalled();
  });
});
