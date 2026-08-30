import { chromium, type FullConfig } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { CONFIG_URL, STORAGE_STATE } from "./playwright.config";

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

  await page.goto(CONFIG_URL, { waitUntil: "domcontentloaded" });
  await page.getByRole("button", { name: "登录" }).click();

  // Casdoor 的登录页。已有会话时它会直接跳回来，所以这两个框可能根本不出现。
  // ⚠️ 这里必须用 waitFor 而不是 isVisible():isVisible() **不接受 timeout**,
  // 它立刻返回当下的可见性 —— 页面还没渲染完就会得到 false,于是静默跳过登录,
  // 最后表现成后面 waitForURL 超时,看上去像是登录失败。
  const usernameBox = page.getByRole("textbox", { name: /username|用户名/i });
  const needsLogin = await usernameBox
    .waitFor({ state: "visible", timeout: 15_000 })
    .then(() => true)
    .catch(() => false);
  if (needsLogin) {
    await usernameBox.fill(username);
    await page.getByRole("textbox", { name: /password|密码/i }).fill(password);
    await page.getByRole("button", { name: /sign in|登录/i }).click();
  }

  // 回到控制台且确实进去了：登出按钮只在已登录时出现。
  await page.waitForURL(new RegExp(`^${CONFIG_URL}`), { timeout: 30_000 });
  await page.getByRole("button", { name: "登出" }).waitFor({ timeout: 30_000 });

  await page.context().storageState({ path: STORAGE_STATE });
  await browser.close();
}

export default globalSetup;
