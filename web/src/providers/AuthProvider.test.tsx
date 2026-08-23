import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test, vi } from "vite-plus/test";
import { clearTokens } from "@/auth";
import { AuthProvider, useAuthState } from "@/providers/AuthProvider";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

const harness = vi.hoisted(() => ({
  authListeners: [] as Array<(error: unknown) => void>,
  accessDeniedListeners: [] as Array<(error: unknown) => void>,
  renewSession: vi.fn(),
  restoreSession: vi.fn(),
  stopRenew: vi.fn(),
  startLogin: vi.fn(),
}));

vi.mock("@/api/transport", () => ({
  onAuthError: (listener: (error: unknown) => void) => {
    harness.authListeners.push(listener);
    return () => {
      const index = harness.authListeners.indexOf(listener);
      if (index >= 0) harness.authListeners.splice(index, 1);
    };
  },
  onAccessDenied: (listener: (error: unknown) => void) => {
    harness.accessDeniedListeners.push(listener);
    return () => {
      const index = harness.accessDeniedListeners.indexOf(listener);
      if (index >= 0) harness.accessDeniedListeners.splice(index, 1);
    };
  },
}));

vi.mock("@/auth/session", () => ({
  renewSession: harness.renewSession,
  restoreSession: harness.restoreSession,
  stopRenew: harness.stopRenew,
}));

vi.mock("@/auth/pkce", () => ({
  buildLogoutUrl: () => "https://identity.example.com/logout",
  startLogin: harness.startLogin,
}));

let latestState: ReturnType<typeof useAuthState> | undefined;

function StateProbe() {
  latestState = useAuthState();
  return null;
}

async function renderProvider(): Promise<Root> {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  await act(async () => {
    root.render(
      <AuthProvider router={{ state: { location: { href: "/" } }, navigate: vi.fn() }}>
        <StateProbe />
      </AuthProvider>,
    );
  });
  return root;
}

describe("AuthProvider recovery lifecycle", () => {
  beforeEach(() => {
    clearTokens();
    latestState = undefined;
    harness.authListeners.length = 0;
    harness.accessDeniedListeners.length = 0;
    vi.clearAllMocks();
    harness.restoreSession.mockResolvedValue(false);
    harness.startLogin.mockResolvedValue(undefined);
    vi.spyOn(console, "warn").mockImplementation(() => {});
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("403 会废弃进行中的 401 recovery，晚到失败不会触发登录", async () => {
    let rejectRenew!: (error: unknown) => void;
    harness.renewSession.mockReturnValue(
      new Promise((_, reject) => {
        rejectRenew = reject;
      }),
    );
    const root = await renderProvider();

    act(() => harness.authListeners[0](new Response(null, { status: 401 })));
    expect(harness.renewSession).toHaveBeenCalledOnce();

    act(() => harness.accessDeniedListeners[0](new Response(null, { status: 403 })));
    expect(latestState).toEqual({ isAuthenticated: false, accessDenied: true });

    await act(async () => {
      rejectRenew(new Error("late renewal failure"));
      await Promise.resolve();
    });
    expect(harness.startLogin).not.toHaveBeenCalled();

    await act(async () => root.unmount());
  });

  test("组件卸载会废弃 recovery，晚到失败不会导航", async () => {
    let rejectRenew!: (error: unknown) => void;
    harness.renewSession.mockReturnValue(
      new Promise((_, reject) => {
        rejectRenew = reject;
      }),
    );
    const root = await renderProvider();

    act(() => harness.authListeners[0](new Response(null, { status: 401 })));
    await act(async () => root.unmount());
    await act(async () => {
      rejectRenew(new Error("renewal completed after unmount"));
      await Promise.resolve();
    });

    expect(harness.startLogin).not.toHaveBeenCalled();
    expect(harness.stopRenew).toHaveBeenCalled();
  });
});
