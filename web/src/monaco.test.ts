/**
 * 回归护栏:monaco 的 AMD loader 必须走同源路径。
 *
 * 一旦有人删掉 `loader.config(...)`,`@monaco-editor/react` 会回落到
 * cdn.jsdelivr.net,在生产 CSP(`script-src 'self'`)下被静默拦截,
 * /edit 与 /history 会永远停在 "Loading..." —— 而网络面板里没有任何报错。
 */
import { expect, test, vi } from "vitest";

const { config } = vi.hoisted(() => ({ config: vi.fn() }));
vi.mock("@monaco-editor/react", () => ({ loader: { config } }));

test("monaco loader 配置为同源 /vs,不回落到 CDN", async () => {
  const { MONACO_VS_PATH } = await import("./monaco");

  expect(config).toHaveBeenCalledWith({ paths: { vs: MONACO_VS_PATH } });
  expect(MONACO_VS_PATH).toBe("/vs");
  // 任何带协议/双斜杠的写法都是跨域,CSP 会拦
  expect(/^(https?:)?\/\//.test(MONACO_VS_PATH)).toBe(false);
});
