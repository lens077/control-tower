import { describe, expect, test } from "vite-plus/test";
import { colorVariables, ground, ink, palettes, state } from "@/styles/tokens";

function flatKeys(value: object, prefix = ""): string[] {
  return Object.entries(value).flatMap(([key, child]) =>
    typeof child === "object" ? flatKeys(child, `${prefix}${key}.`) : [`${prefix}${key}`],
  );
}

describe("palettes", () => {
  test("白天与黑夜两套色板的令牌一一对应,不会有一边漏定义", () => {
    expect(flatKeys(palettes.dark)).toEqual(flatKeys(palettes.light));
  });

  test("所有色值都是具体的十六进制色,MUI 与 Monaco 需要能直接解析", () => {
    for (const mode of ["light", "dark"] as const) {
      for (const group of Object.values(palettes[mode])) {
        for (const color of Object.values(group)) expect(color).toMatch(/^#[0-9A-F]{6}([0-9A-F]{2})?$/);
      }
    }
  });
});

describe("组件用的令牌", () => {
  test("指向 CSS 变量,切换主题时组件无需重新取值", () => {
    expect(ink.strong).toBe("var(--cc-ink-strong)");
    expect(ground.lineStrong).toBe("var(--cc-ground-line-strong)");
    expect(state.activeSoft).toBe("var(--cc-state-active-soft)");
  });

  test("colorVariables 为每个令牌给出当前模式的具体值", () => {
    const light = colorVariables("light");
    const dark = colorVariables("dark");
    expect(light["--cc-ink-strong"]).toBe(palettes.light.ink.strong);
    expect(dark["--cc-ink-strong"]).toBe(palettes.dark.ink.strong);
    expect(Object.keys(dark)).toEqual(Object.keys(light));
    for (const key of flatKeys(palettes.light)) {
      const [group, name] = key.split(".");
      const variable = `--cc-${group}-${name.replace(/[A-Z]/g, (c) => `-${c.toLowerCase()}`)}`;
      expect(light[variable]).toBeDefined();
    }
    expect(light["--cc-shadow-popup"]).toBeDefined();
    expect(dark["--cc-shadow-popup"]).not.toBe(light["--cc-shadow-popup"]);
  });
});
