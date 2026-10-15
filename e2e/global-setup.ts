import { chromium, type FullConfig, type Page } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { CONFIG_URL, STORAGE_STATE } from "./playwright.config";
import { navigate } from "./nav";

/**
 * 登录失败时把「页面当时长什么样」打出来。
 *
 * 2026-10-16 的教训：global-setup 挂在 `waitForURL` 上，日志只有一句
 * `Timeout 30000ms exceeded`，完全看不出是**凭据错**、**Casdoor 拒绝回调**、
 * 还是**压根没找到登录表单** —— 三种原因的修法完全不同（改 Secrets / 加白名单 /
 * 改选择器），只能靠猜，一圈试下来要好几个 CI 周期。
 * 所以这里在抛错前把 URL 与可见文本落盘并打到 stdout，让日志自己说清楚。
 */
async function dumpLoginFailure(page: Page, phase: string): Promise<string> {
  const url = page.url();
  let body = "";
  try {
    body = ((await page.locator("body").innerText({ timeout: 5_000 })) ?? "").replace(/\s+/g, " ");
  } catch {
    body = "（读不到正文）";
  }

  const onCasdoor = url.includes("casdoor.");
  let hint: string;
  if (/doesn't exist in the allowed Redirect URI/i.test(body)) {
    hint = "Casdoor 回调白名单里没有这个 origin —— 去 Casdoor 应用 ecommerce 加 redirect_uri";
  } else if (/incorrect|invalid|错误|失败/i.test(body)) {
    hint = "像是 Casdoor 拒了这次登录 —— 先核对 Secrets 里的 E2E_USERNAME / E2E_PASSWORD";
  } else if (onCasdoor) {
    hint = "仍停在 Casdoor 上，没跳回控制台 —— 看上面的正文判断是哪一步卡住";
  } else {
    hint = "不在 Casdoor 上 —— 检查 CONFIG_URL 与目标站点";
  }

  const report =
    `\n===== global-setup 登录失败 (${phase}) =====\n` +
    `CONFIG_URL : ${CONFIG_URL}\n` +
    `当前 URL   : ${url}\n` +
    `正文片段   : ${body.slice(0, 600)}\n` +
    `判断       : ${hint}\n` +
    `=============================================\n`;
  console.log(report);
  // 同时落盘，方便从上传的 artifact 里看（test-results 会被 workflow 上传）。
  try {
    mkdirSync("./test-results", { recursive: true });
    writeFileSync("./test-results/global-setup-failure.txt", report);
  } catch {
    /* 落盘失败不影响抛错 */
  }
  return hint;
}

/**
 * 登录一次，把 **Casdoor 那边的会话 Cookie** 存下来给所有用例复用。
 *
 * 注意这里存的不是控制台自己的登录态：控制台把 access token 只放在内存里，
 * 整页加载后靠隐藏 iframe 向 Casdoor 做 prompt=none 的静默续期把它换回来。
 * 所以「存 Casdoor Cookie + 每个用例整页打开」这套组合，顺带把静默续期这条链路
 * 也持续回归了 —— 它一旦被 CSP 拦掉（2026-08-30 就发生过），用例会立刻红。
 */
async function globalSetup(_config: FullConfig) {
  const username = process.env.E2E_USERNAME;
  const password = process.env.E2E_PASSWORD;
  if (!username || !password) {
    throw new Error(
      "缺少 E2E_USERNAME / E2E_PASSWORD。凭据不进仓库，运行时从环境变量给：\n" +
        "  E2E_USERNAME=<账号> E2E_PASSWORD=<口令> pnpm test",
    );
  }

  mkdirSync("./.auth", { recursive: true });
  const browser = await chromium.launch();
  const page = await browser.newPage();

  try {
    // 隧道抖动偶发让首屏导航超时；这里用带重试的 navigate（见 nav.ts）。
    // globalSetup 不受单条用例 90s 超时约束，所以多给几次重试、单次超时也放宽到 30s。
    await navigate(page, CONFIG_URL, { retries: 3, timeout: 30_000 });
    // ⚠️ 必须限定在 <main> 里。0.2.16 的控制台改版给 header 也放了一个「登录」按钮
    // （`web/src/routes/__root.tsx`：header 一个、SignedOut 落地页一个，点击行为相同），
    // 不限定作用域会撞 Playwright 严格模式：`resolved to 2 elements`，
    // 从 2026-09-18 起每次 6 小时巡检都挂在这一行。
    // 取 <main> 里那个而不是 .first()：未登录落地页的 CTA 才是这条链路要走的入口。
    await page.getByRole("main").getByRole("button", { name: "登录" }).click();

    // Casdoor 的登录页。已有会话时它会直接跳回来，所以这两个框可能根本不出现。
    // ⚠️ 这里必须用 waitFor 而不是 isVisible():isVisible() **不接受 timeout**,
    // 它立刻返回当下的可见性 —— 页面还没渲染完就会得到 false,于是静默跳过登录,
    // 最后表现成后面 waitForURL 超时,看上去像是登录失败。
    const usernameBox = page.getByRole("textbox", { name: /username|用户名/i });
    const needsLogin = await usernameBox
      .waitFor({ state: "visible", timeout: 15_000 })
      .then(() => true)
      .catch(() => false);
    console.log(`[global-setup] Casdoor 登录表单: ${needsLogin ? "已出现" : "未出现（沿用已有会话）"}`);

    if (needsLogin) {
      await usernameBox.fill(username);
      await page.getByRole("textbox", { name: /password|密码/i }).fill(password);
      await page.getByRole("button", { name: /sign in|登录/i }).click();
      // 给 Casdoor 一点时间渲染结果：凭据错会原地弹错误、成功会 302 回控制台。
      // 不留这点时间的话，下面 waitForURL 抓到的正文还是登录表单，诊断信息就没用了。
      await page.waitForTimeout(3_000);
    }

    // 回到控制台且确实进去了：登出按钮只在已登录时出现。
    await page.waitForURL(new RegExp(`^${CONFIG_URL}`), { timeout: 30_000 });
    await page.getByRole("button", { name: "登出" }).waitFor({ timeout: 30_000 });
  } catch (err) {
    const hint = await dumpLoginFailure(page, "waitForURL / 登出按钮");
    await browser.close();
    throw new Error(`${hint}\n\n原始错误: ${String(err)}`);
  }

  await page.context().storageState({ path: STORAGE_STATE });
  await browser.close();
}

export default globalSetup;
