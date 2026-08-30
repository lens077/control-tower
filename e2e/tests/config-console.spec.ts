import { expect, test, type ConsoleMessage, type Page } from "@playwright/test";

/**
 * config 服务 + Web 控制台的端到端用例。
 *
 * 每条用例都对应一个**真实发生过的故障**，注释里写明是哪一个 —— 这样将来它变红时，
 * 读的人立刻知道自己踩回了哪个坑，而不是只看到一条断言失败。
 */

/** 收集控制台里的 error，用来断言「页面没有静默报错」。 */
function collectErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on("console", (msg: ConsoleMessage) => {
    if (msg.type() === "error") errors.push(msg.text());
  });
  page.on("pageerror", (err) => errors.push(String(err)));
  return errors;
}

/** 等控制台完成静默续期、确认处于已登录状态。 */
async function expectSignedIn(page: Page) {
  await expect(page.getByRole("button", { name: "登出" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("请先登录以管理配置")).toHaveCount(0);
}

test("整页加载后能靠静默续期保持登录", async ({ page }) => {
  // 回归 2026-08-30:CSP 少了 frame-src,静默续期的隐藏 iframe(Casdoor prompt=none)
  // 被 default-src 'self' 拦掉 —— 每次刷新都退回「请先登录」,深链永远打不开,
  // 而网络面板一片正常。
  const errors = collectErrors(page);

  await page.goto("/");
  await expectSignedIn(page);

  await page.reload();
  await expectSignedIn(page);

  expect(errors.filter((e) => /Content Security Policy|frame-src/i.test(e))).toEqual([]);
});

test("深链直接打开 /edit 能渲染出编辑器且带内容", async ({ page }) => {
  // 回归 2026-08-29:@monaco-editor/react 默认从 cdn.jsdelivr.net 取 AMD loader,
  // 被 script-src 'self' 拦掉 —— 编辑器永远停在兜底占位符 "Loading...",
  // 而网络面板看不到任何失败条目(CSP 拦截发生在发请求之前)。
  const errors = collectErrors(page);

  await page.goto("/edit?ns=product&env=dev&key=bootstrap.yaml");
  await expectSignedIn(page);

  const editor = page.locator(".monaco-editor").first();
  await expect(editor).toBeVisible({ timeout: 45_000 });
  // 只断言「有编辑器」不够:加载失败时占位符也在同一个位置。必须看到真实内容行。
  await expect(page.locator(".view-line").first()).toBeVisible({ timeout: 45_000 });
  await expect(page.getByText("Loading...", { exact: true })).toHaveCount(0);

  // monaco 的资源必须来自同源 /vs,回落到 CDN 就等于这条修复被撤销了。
  const loader = await page.request.get("/vs/loader.js");
  expect(loader.status()).toBe(200);

  expect(errors.filter((e) => /jsdelivr|Monaco initialization/i.test(e))).toEqual([]);
});

test("历史页的 diff 编辑器可渲染", async ({ page }) => {
  await page.goto("/history?ns=product&env=dev&key=bootstrap.yaml");
  await expectSignedIn(page);
  await expect(page.locator(".monaco-diff-editor, .monaco-editor").first()).toBeVisible({
    timeout: 45_000,
  });
});

test("系统页的指标后端可用,且至少一组曲线取得到数据", async ({ page }) => {
  // 回归 2026-08-29/30 的指标链路三连:域名改名后 collector 还在推旧域名(数据全丢)、
  // VictoriaMetrics 没开 usePrometheusNaming(查询成功但零序列)、主机指标缺 DaemonSet。
  const responses: Array<Record<string, unknown>> = [];
  page.on("response", async (res) => {
    if (!res.url().includes("QueryMetrics")) return;
    try {
      responses.push((await res.json()) as Record<string, unknown>);
    } catch {
      /* 非 JSON 响应忽略,由下面的断言兜底 */
    }
  });

  await page.goto("/system");
  await expectSignedIn(page);

  // 后端不可用时前端会显式提示,而不是画空图 —— 出现这句话就是没接上指标后端。
  await expect(page.getByText(/未配置指标|metrics backend/i)).toHaveCount(0);

  await expect
    .poll(() => responses.length, { timeout: 60_000, message: "没有收到 QueryMetrics 响应" })
    .toBeGreaterThan(0);

  const withData = responses.some((body) => {
    if (body.metricsBackendAvailable === false) return false;
    const results = (body.results ?? []) as Array<{ lines?: Array<{ points?: unknown[] }> }>;
    return results.some((r) => (r.lines ?? []).some((l) => (l.points ?? []).length > 0));
  });
  expect(withData, "QueryMetrics 全部返回空序列 —— 指标名/标签可能又对不上了").toBe(true);
});

test("界面上不出现未翻译的原始 i18n key", async ({ page }) => {
  // 回归 2026-08-29:代码引用了从不存在的 common: 命名空间,i18next 把前缀剥掉后
  // 按钮上直接显示 action.save / action.delete 这样的原始 key。
  await page.goto("/");
  await expectSignedIn(page);

  const body = (await page.locator("body").innerText()) ?? "";
  const rawKeys = body.match(/\b(action|edit|browser|system|tokens)\.[a-zA-Z]+\b/g) ?? [];
  expect(rawKeys, `界面上出现了原始 key: ${rawKeys.join(", ")}`).toEqual([]);
});

test("各页面都不刷 CSP 违规或未捕获异常", async ({ page }) => {
  const errors = collectErrors(page);

  for (const path of ["/", "/connections", "/tokens", "/system"]) {
    await page.goto(path);
    await expectSignedIn(page);
  }

  // 允许业务性质的 4xx 提示,只卡死 CSP 违规与脚本异常这两类「配置/构建层面的坏」。
  const fatal = errors.filter((e) => /Content Security Policy|Refused to|Uncaught/i.test(e));
  expect(fatal, `控制台出现致命错误:\n${fatal.join("\n")}`).toEqual([]);
});
