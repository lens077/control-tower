import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, test, vi } from "vite-plus/test";
import {
  COLOR_MODE_STORAGE_KEY,
  readPreference,
  resolveColorMode,
  setPreference,
  subscribePreference,
  useColorMode,
} from "@/styles/color-mode";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

describe("readPreference", () => {
  beforeEach(() => localStorage.clear());

  test("没有存过偏好时默认跟随系统", () => {
    expect(readPreference()).toBe("system");
  });

  test("读回显式选择的白天或黑夜", () => {
    localStorage.setItem(COLOR_MODE_STORAGE_KEY, "dark");
    expect(readPreference()).toBe("dark");
    localStorage.setItem(COLOR_MODE_STORAGE_KEY, "light");
    expect(readPreference()).toBe("light");
  });

  test("存储里是无法识别的值时退回跟随系统", () => {
    localStorage.setItem(COLOR_MODE_STORAGE_KEY, "sepia");
    expect(readPreference()).toBe("system");
  });
});

describe("resolveColorMode", () => {
  test("跟随系统时由系统是否为深色决定", () => {
    expect(resolveColorMode("system", true)).toBe("dark");
    expect(resolveColorMode("system", false)).toBe("light");
  });

  test("显式选择覆盖系统设置", () => {
    expect(resolveColorMode("light", true)).toBe("light");
    expect(resolveColorMode("dark", false)).toBe("dark");
  });
});

describe("useColorMode", () => {
  let root: Root | null = null;
  let host: HTMLDivElement | null = null;

  afterEach(() => {
    act(() => root?.unmount());
    host?.remove();
    root = null;
    host = null;
    localStorage.clear();
    vi.unstubAllGlobals();
  });

  /** 可控的 prefers-color-scheme:改 dark 后派发 change 事件,模拟用户切换系统外观。 */
  function stubSystemScheme(initialDark: boolean) {
    const listeners = new Set<() => void>();
    const query = {
      matches: initialDark,
      addEventListener: (_: string, listener: () => void) => listeners.add(listener),
      removeEventListener: (_: string, listener: () => void) => listeners.delete(listener),
    };
    vi.stubGlobal("matchMedia", () => query);
    return (dark: boolean) => {
      query.matches = dark;
      for (const listener of listeners) listener();
    };
  }

  function renderMode(): () => string {
    function Probe() {
      const { preference, mode } = useColorMode();
      return <span>{`${preference}:${mode}`}</span>;
    }
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    act(() => root!.render(<Probe />));
    return () => host!.textContent ?? "";
  }

  test("默认跟随系统,系统切换深浅色时实时生效", () => {
    const setSystemDark = stubSystemScheme(false);
    const text = renderMode();
    expect(text()).toBe("system:light");

    act(() => setSystemDark(true));
    expect(text()).toBe("system:dark");
  });

  test("显式选了白天后,系统切到深色也不跟", () => {
    const setSystemDark = stubSystemScheme(false);
    const text = renderMode();

    act(() => setPreference("light"));
    act(() => setSystemDark(true));
    expect(text()).toBe("light:light");

    act(() => setPreference("system"));
    expect(text()).toBe("system:dark");
  });
});

describe("setPreference", () => {
  afterEach(() => localStorage.clear());

  test("持久化选择并通知订阅者", () => {
    const listener = vi.fn();
    const unsubscribe = subscribePreference(listener);

    setPreference("dark");

    expect(localStorage.getItem(COLOR_MODE_STORAGE_KEY)).toBe("dark");
    expect(readPreference()).toBe("dark");
    expect(listener).toHaveBeenCalledTimes(1);
    unsubscribe();
  });

  test("选回跟随系统时清掉存储,而不是写入一个固定值", () => {
    setPreference("light");
    setPreference("system");
    expect(localStorage.getItem(COLOR_MODE_STORAGE_KEY)).toBeNull();
    expect(readPreference()).toBe("system");
  });
});
