import { type Page } from "@playwright/test";

export interface NavOptions {
  /** 额外重试次数（不含首次）。默认 2。 */
  retries?: number;
  /** 单次 goto 超时（毫秒）。默认 20_000。 */
  timeout?: number;
}

/**
 * 隧道/CDN 抖动时 `page.goto` 偶发超时（多次 CI 失败：navigating to config.apikv.com
 * 超过 30s/90s 直接把整条用例挂红）。根因是默认 `waitUntil: "load"` 要等**所有**子资源
 * （图片、字体、iframe、Monaco 的 /vs 资源）全部加载完，任何一段被隧道拖慢都会超时。
 *
 * 这里改成 `domcontentloaded`（SPA 首屏 HTML 一到就落定，不等 load 事件）+ 有限重试 +
 * 落定后等一个真实元素（确认 HTML 真的到了，过滤掉 200 但空白的异常响应），把瞬时抖动
 * 过滤掉，而不是让用例红。
 */
async function waitForContent(page: Page): Promise<void> {
  // body 下至少出现一个元素：SPA 的 index.html 里就有 <div id="app">，能确认文档真的到达。
  await page.locator("body *").first().waitFor({ state: "attached", timeout: 10_000 });
}

export async function navigate(page: Page, url: string, opts: NavOptions = {}): Promise<void> {
  const retries = opts.retries ?? 2;
  const timeout = opts.timeout ?? 20_000;
  let lastErr: unknown;
  for (let attempt = 1; attempt <= retries + 1; attempt++) {
    try {
      await page.goto(url, { waitUntil: "domcontentloaded", timeout });
      await waitForContent(page);
      return;
    } catch (err) {
      lastErr = err;
      if (attempt <= retries) {
        console.warn(`[nav] goto ${url} 第 ${attempt}/${retries + 1} 次失败，1s 后重试…`);
        await page.waitForTimeout(1_000).catch(() => undefined);
      }
    }
  }
  throw lastErr;
}
