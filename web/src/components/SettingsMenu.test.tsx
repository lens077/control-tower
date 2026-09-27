import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeAll, describe, expect, test } from "vite-plus/test";
import { SettingsMenu } from "@/components/SettingsMenu";
import { i18next, initI18n } from "@/i18n";
import { COLOR_MODE_STORAGE_KEY } from "@/styles/color-mode";
import configEn from "@/locales/en/config.json";
import configZh from "@/locales/zh-CN/config.json";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

let root: Root | null = null;
let host: HTMLDivElement | null = null;

beforeAll(async () => {
  await initI18n({ ns: "config", resources: { "zh-CN": configZh, en: configEn }, locale: "zh-CN" });
  await i18next.changeLanguage("zh-CN");
});

afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  root = null;
  host = null;
  localStorage.clear();
  document.body.innerHTML = "";
});

function renderMenu() {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  act(() => root!.render(<SettingsMenu />));
}

function openMenu() {
  const trigger = document.querySelector<HTMLButtonElement>('button[aria-label="设置"]');
  expect(trigger).not.toBeNull();
  act(() => trigger!.click());
}

function option(label: string): HTMLButtonElement {
  const button = [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === label);
  expect(button, `找不到「${label}」选项`).toBeDefined();
  return button!;
}

describe("SettingsMenu", () => {
  test("外观默认选中「跟随系统」", () => {
    renderMenu();
    openMenu();

    expect(option("跟随系统").getAttribute("aria-pressed")).toBe("true");
    expect(option("白天").getAttribute("aria-pressed")).toBe("false");
    expect(option("黑夜").getAttribute("aria-pressed")).toBe("false");
  });

  test("选「黑夜」后持久化,选回「跟随系统」清掉偏好", () => {
    renderMenu();
    openMenu();

    act(() => option("黑夜").click());
    expect(localStorage.getItem(COLOR_MODE_STORAGE_KEY)).toBe("dark");
    expect(option("黑夜").getAttribute("aria-pressed")).toBe("true");

    act(() => option("跟随系统").click());
    expect(localStorage.getItem(COLOR_MODE_STORAGE_KEY)).toBeNull();
    expect(option("跟随系统").getAttribute("aria-pressed")).toBe("true");
  });
});
