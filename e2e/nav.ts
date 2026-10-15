import { type Page } from "@playwright/test";

export interface NavOptions {
  /** 额外重试次数（不含首次）。默认 2。 */
  retries?: number;
  /** 单次 goto 超时（毫秒）。默认 20_000。 */
  timeout?: number;
  /**
   * 导航后是否校验「这真的是控制台页面」。默认 true。
   *
   * 只导航到 console 以外的页面（比如将来要直接开 Casdoor 登录页）时传 false。
   */
  expectConsole?: boolean;
}

/**
 * 隧道/CDN 抖动时 `page.goto` 偶发超时（多次 CI 失败：navigating to config.apikv.com
 * 超过 30s/90s 直接把整条用例挂红）。根因是默认 `waitUntil: "load"` 要等**所有**子资源
 * （图片、字体、iframe、Monaco 的 /vs 资源）全部加载完，任何一段被隧道拖慢都会超时。
 *
 * 这里改成 `domcontentloaded`（SPA 首屏 HTML 一到就落定，不等 load 事件）+ 有限重试，
 * 把瞬时抖动过滤掉，而不是让用例红。
 */

/** 页面到了，但**不是**控制台：这类失败是确定的，重试多少次都一样，直接抛。 */
class NotConsolePageError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "NotConsolePageError";
  }
}

function describeBadResponse(status: number, text: string): string {
  // 按 AGENTS.md §7.1：纯文本 `404 page not found` 说明请求**根本没到集群**，
  // 是 Pangolin 隧道里没有这条资源；到了集群根路径会返回应用层 JSON `ROUTE_NOT_FOUND`。
  if (text.includes("404 page not found")) {
    return "Pangolin/边缘网关的纯文本 404 —— 请求没到控制台。自定义域名多半还是 A 记录指向隧道 IP，没有 CNAME 到 EdgeOne Pages。";
  }
  if (text.includes("ROUTE_NOT_FOUND")) {
    return "网关应用层的 ROUTE_NOT_FOUND —— 请求进了集群但主机/路径没匹配上。";
  }
  if (!text) {
    return "响应体为空 —— 边缘直接掐断了连接（隧道未建、TLS 握手后无数据）。";
  }
  return `响应看起来是错误页（HTTP ${status}）。`;
}

/**
 * 导航后确认拿到的是控制台 SPA 而不是某个错误页。
 *
 * 为什么要这一步：2026-10-15 的 CI 连续挂在
 * `getByRole('main').getByRole('button', { name: '登录' }).click()` 超时 30s。
 * 真正的原因是 `egde-one.apikv.com` 当时返回 19 字节的 `404 page not found`——
 * 页面**加载成功了**（所以 goto 不报错、重试也不会重试），只是根本没有 `<main>`。
 * 没有这道校验时，报错停在「点不到登录按钮」，把人往完全错误的方向带：
 * 看着像控制台改版把按钮改没了，实际是 DNS/隧道根本没通。
 */
async function assertConsolePage(page: Page, url: string, status: number): Promise<void> {
  const probe = await page.evaluate(() => ({
    hasAppRoot: !!document.getElementById("app"),
    title: document.title || "",
    text: (document.body?.innerText || "").trim().slice(0, 160),
  }));

  // 控制台的 index.html 里就带着 <div id="app">，不依赖 React 挂载完成，
  // 所以这是「文档真的来自控制台」的可靠判据，也不会被前端渲染慢拖成超时。
  if (probe.hasAppRoot) return;

  throw new NotConsolePageError(
    `[nav] ${url} 返回的不是控制台页面（HTML 里没有 #app 根节点）。\n` +
      `  HTTP 状态: ${status || "(未知)"}\n` +
      `  页面标题: ${probe.title || "(空)"}\n` +
      `  响应正文: ${probe.text || "(空)"}\n` +
      `  判断: ${describeBadResponse(status, probe.text)}\n` +
      `  供排查:\n` +
      `   · 自定义域名是否 CNAME 到 EdgeOne Pages（而不是 A 记录指向 Pangolin 隧道 IP）\n` +
      `   · Pangolin 里是否有这条资源（见 AGENTS.md §7.1）\n` +
      `   · 目标是否把请求挡在了控制台之外（Casdoor / 网关 / WAF）`,
  );
}

export async function navigate(page: Page, url: string, opts: NavOptions = {}): Promise<void> {
  const retries = opts.retries ?? 2;
  const timeout = opts.timeout ?? 20_000;
  const expectConsole = opts.expectConsole ?? true;
  let lastErr: unknown;

  for (let attempt = 1; attempt <= retries + 1; attempt++) {
    try {
      const response = await page.goto(url, { waitUntil: "domcontentloaded", timeout });
      // body 下至少出现一个元素：确认文档真的到达（过滤掉 200 但空白的异常响应）。
      await page.locator("body *").first().waitFor({ state: "attached", timeout: 10_000 });
      if (expectConsole) {
        await assertConsolePage(page, url, response?.status() ?? 0);
      }
      return;
    } catch (err) {
      // 「页面到了但不是控制台」是确定性结论，重试没有意义，立刻抛出。
      if (err instanceof NotConsolePageError) throw err;

      lastErr = err;
      if (attempt <= retries) {
        console.warn(`[nav] goto ${url} 第 ${attempt}/${retries + 1} 次失败，1s 后重试…`);
        await page.waitForTimeout(1_000).catch(() => undefined);
      }
    }
  }
  throw lastErr;
}
