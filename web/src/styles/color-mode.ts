import { useSyncExternalStore } from "react";

/**
 * 外观偏好:跟随系统 / 白天 / 黑夜。
 *
 * 默认跟随系统:存储里没有值就是 "system"。选回「跟随系统」时删掉存储项而不是写入
 * "system",这样以后调整默认值时,没有主动选过的人会一起跟着变。
 */
export type ColorModePreference = "system" | "light" | "dark";
export type ColorMode = "light" | "dark";

export const COLOR_MODE_STORAGE_KEY = "config-center-color-mode";

const DARK_QUERY = "(prefers-color-scheme: dark)";

export function readPreference(): ColorModePreference {
  const saved = localStorage.getItem(COLOR_MODE_STORAGE_KEY);
  return saved === "light" || saved === "dark" ? saved : "system";
}

export function resolveColorMode(preference: ColorModePreference, systemDark: boolean): ColorMode {
  if (preference === "system") return systemDark ? "dark" : "light";
  return preference;
}

const listeners = new Set<() => void>();

function notify() {
  for (const listener of listeners) listener();
}

export function setPreference(preference: ColorModePreference): void {
  if (preference === "system") localStorage.removeItem(COLOR_MODE_STORAGE_KEY);
  else localStorage.setItem(COLOR_MODE_STORAGE_KEY, preference);
  notify();
}

/** 订阅偏好变化,包括其它标签页里改的(storage 事件只在别的标签页触发)。 */
export function subscribePreference(listener: () => void): () => void {
  listeners.add(listener);
  const onStorage = (event: StorageEvent) => {
    if (event.key === COLOR_MODE_STORAGE_KEY || event.key === null) listener();
  };
  window.addEventListener("storage", onStorage);
  return () => {
    listeners.delete(listener);
    window.removeEventListener("storage", onStorage);
  };
}

function darkQuery(): MediaQueryList | null {
  return typeof window.matchMedia === "function" ? window.matchMedia(DARK_QUERY) : null;
}

function subscribeSystem(listener: () => void): () => void {
  const query = darkQuery();
  if (!query) return () => {};
  query.addEventListener("change", listener);
  return () => query.removeEventListener("change", listener);
}

function systemPrefersDark(): boolean {
  return darkQuery()?.matches ?? false;
}

/** 当前偏好与实际生效的配色。偏好是「跟随系统」时,系统切换深浅色会实时生效。 */
export function useColorMode(): {
  preference: ColorModePreference;
  mode: ColorMode;
  setPreference: (preference: ColorModePreference) => void;
} {
  const preference = useSyncExternalStore(subscribePreference, readPreference);
  const systemDark = useSyncExternalStore(subscribeSystem, systemPrefersDark);
  return { preference, mode: resolveColorMode(preference, systemDark), setPreference };
}
