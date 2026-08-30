import { expect, test, type Page } from "@playwright/test";
import { STORAGE_STATE } from "../playwright.config";

/**
 * 配置中心的**写路径**：新建 → 保存 → 再保存 → 回滚 → 删除。
 *
 * 为什么必须单独有这一组：读路径全绿只能说明「页面画得出来」。配置中心的核心价值是写，
 * 而写链路多穿过三层读路径碰不到的东西 —— 数据库事务与版本号、schema 校验（`enforce` 模式）、
 * 回滚（把旧版本内容写成新版本，而不是删历史）。编辑器能渲染 ≠ 保存能成功。
 *
 * 数据隔离：写在专用命名空间 `e2e` 里。namespace 输入框是 freeSolo 的（`routes/index.tsx`
 * 注释写明「既能从已有里选，也能直接输入一个新的用于建首个 key」），而删掉最后一个 key 时
 * namespace 会随之消失 —— 所以这组用例跑完不留任何痕迹，也不碰真实业务命名空间。
 */

const NS = "e2e";
const ENV = "dev";
// 每次跑用不同的 key：上一次跑到一半挂掉留下的残骸不会让这次误判「已存在」，
// 本地与 CI 万一撞在一起也不会互相踩。
const KEY = `smoke-${Date.now()}.yaml`;

const EDIT_URL = `/edit?ns=${NS}&env=${ENV}&key=${KEY}`;

async function expectSignedIn(page: Page) {
  await expect(page.getByRole("button", { name: "登出" })).toBeVisible({ timeout: 30_000 });
}

/**
 * 等编辑器把**已有内容**加载出来。
 *
 * 这一步不能省：`/edit` 是先渲染空编辑器、拿到 GetKey 响应后再由 useEffect 回填内容的。
 * 只等 `.monaco-editor` 可见就开始敲字，回填会把刚敲的覆盖掉，最后表现成
 * 「保存了但版本号没动」——排查时会误以为是保存接口的问题。
 */
async function waitForEditorContent(page: Page, expected: string) {
  await expect(page.locator(".view-line").first()).toHaveText(expected, { timeout: 45_000 });
}

/**
 * 替换编辑器内容。
 *
 * 走 Monaco 的 model API 而不是敲键盘，是被两次实测逼出来的：
 *   - `page.keyboard.press("ControlOrMeta+a")`：按键落在文档上而不是编辑器里，
 *     全选没生效，新内容被**追加**在旧内容后面（实测拿到 `e2e: v1e2e: v2`）；
 *   - 改成点编辑器自己的 `textarea.inputarea`：那是个隐藏元素，Playwright 的
 *     可操作性检查过不去，`click()` 直接超时。
 *
 * `setValue` 同样会触发 `onDidChangeModelContent` → 组件的 onChange → 校验与保存按钮状态，
 * 也就是说应用侧代码路径与人工输入一致；差别只是不再验证 Monaco 自己的按键处理，
 * 而那是上游的事，不是本仓要守的东西。
 */
async function replaceEditorContent(page: Page, text: string) {
  await page.evaluate((value) => {
    const monaco = (window as unknown as { monaco?: any }).monaco;
    const model = monaco?.editor?.getModels?.()?.[0];
    if (!model) throw new Error("拿不到 Monaco model —— 编辑器没挂载成功");
    model.setValue(value);
  }, text);
  // 敲完先确认编辑器里就是这段内容,再往下走。省掉这条的话,一旦输入没落进去,
  // 失败会推迟到「版本号不对」那里,指向一个错误的方向。
  await waitForEditorContent(page, text);
  // 保存按钮在格式校验不通过时是禁用的（`edit.saveBlocked`）。
  await expect(page.getByRole("button", { name: "保存" })).toBeEnabled({ timeout: 15_000 });
}

/** 点保存并等版本号落到期望值；失败时优先把页面上的错误条报出来。 */
async function saveAndExpectVersion(page: Page, version: string) {
  await page.getByRole("button", { name: "保存" }).click();
  const error = page.locator(".MuiAlert-standardError");
  await expect(error, "保存报错了(`edit.saveFailed`),看红条内容").toHaveCount(0, {
    timeout: 15_000,
  });
  await expect(versionChip(page)).toHaveText(version, { timeout: 30_000 });
}

/**
 * 把浏览页切到本组用例的命名空间/环境。
 *
 * ⚠️ 不能用 `/?ns=…&env=…` 走 URL：浏览页的命名空间与环境读的是 valtio store
 * （`store/editor.ts`），不是 search params。直接拼 URL 打开，页面显示的仍是默认命名空间，
 * 断言会以「key 不在列表里」的形式失败，而真实原因是根本没切过去。
 */
async function selectNamespace(page: Page) {
  await page.getByLabel("命名空间").fill(NS);
  await page.getByLabel("环境").fill(ENV);
}

/** 头部那颗版本 Chip：新建时是「新建」，存过之后是 v1/v2/…… */
function versionChip(page: Page) {
  return page.locator(".MuiChip-label").filter({ hasText: /^(v\d+|新建)$/ });
}

test("配置写路径:新建 → 保存 → 再保存 → 回滚 → 删除", async ({ page }) => {
  await page.goto("/");
  await expectSignedIn(page);

  await test.step("在新命名空间里创建 key", async () => {
    await selectNamespace(page);
    await page.getByRole("button", { name: "新建 Key" }).click();

    const dialog = page.getByRole("dialog");
    await dialog.getByRole("textbox", { name: /^Key/ }).fill(KEY);
    await dialog.getByRole("button", { name: "创建并编辑" }).click();

    await page.waitForURL(new RegExp(`key=${KEY}`), { timeout: 30_000 });
    // 「新建」说明后端确实没有这个 key —— 如果残骸还在，这里会是 v1 而不是新建。
    await expect(versionChip(page)).toHaveText("新建");
    await expect(page.locator(".monaco-editor")).toBeVisible({ timeout: 45_000 });
  });

  await test.step("首次保存产生 v1", async () => {
    await replaceEditorContent(page, "e2e: v1");
    await saveAndExpectVersion(page, "v1");
  });

  await test.step("新 key 出现在浏览列表里", async () => {
    // 只看编辑页的版本号不够：那只证明写进去了，不证明 ListKeys 查得到。
    await page.goto("/");
    await expectSignedIn(page);
    await selectNamespace(page);
    await expect(page.getByText(KEY, { exact: false })).toBeVisible({ timeout: 30_000 });
  });

  await test.step("二次保存产生 v2", async () => {
    await page.goto(EDIT_URL);
    await expectSignedIn(page);
    // 先等 v1 的内容回填完 —— 这既是「读回来的确实是刚存的」这条断言,
    // 也把下面的输入与回填的竞态挡掉。
    await waitForEditorContent(page, "e2e: v1");

    await replaceEditorContent(page, "e2e: v2");
    await saveAndExpectVersion(page, "v2");
  });

  await test.step("回滚到 v1:内容回退,版本前进到 v3", async () => {
    await page.getByRole("button", { name: "历史" }).click();
    await page.waitForURL(/\/history/, { timeout: 30_000 });

    // 先选中 v1。不选的话「回滚到 vX」指向的是当前版本，按钮是禁用的。
    // ⚠️ 不能用 `filter({ hasText: /^v1$/ })`：列表项里除了版本号还有时间、作者、备注，
    // 锚定整项文本的正则永远匹配不上（实测直接卡到超时）。要按**项内那个版本号元素**筛。
    await page
      .getByRole("listitem")
      .filter({ has: page.getByText("v1", { exact: true }) })
      .first()
      .click();
    await page.getByRole("button", { name: "回滚到 v1" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "确认回滚" }).click();

    await page.waitForURL(/\/edit/, { timeout: 30_000 });
    // 回滚的语义是「把 v1 的内容写成新版本」，历史不删除 —— 所以是 v3 而不是回到 v1。
    // 断言版本号本身很重要：如果哪天实现改成「删掉 v2、退回 v1」，这条会红。
    await expect(versionChip(page)).toHaveText("v3", { timeout: 30_000 });
    await expect(page.locator(".view-line").first()).toHaveText("e2e: v1", { timeout: 30_000 });
  });

  await test.step("删除后从列表消失", async () => {
    // ⚠️ 删除**没有二次确认对话框**，点下去就没了。
    await page.getByRole("button", { name: "删除" }).click();
    await page.waitForURL(/\/$|\/\?/, { timeout: 30_000 });

    await page.goto("/");
    await expectSignedIn(page);
    await selectNamespace(page);
    // 等列表刷新完再断言「不存在」,否则可能只是还没加载出来。
    await expect(page.getByText("smoke-", { exact: false })).toHaveCount(0, { timeout: 30_000 });
  });
});

// 上面任何一步挂掉都会留下一个 key，下次跑虽然用新 key 不受影响，但残骸会一直堆在
// `e2e` 命名空间里。这里尽力清一次，清不掉也不让它把测试结果染红（真正的失败在上面）。
test.afterAll(async ({ browser }) => {
  const context = await browser.newContext({ storageState: STORAGE_STATE });
  const page = await context.newPage();
  try {
    await page.goto(EDIT_URL, { waitUntil: "domcontentloaded" });
    const remove = page.getByRole("button", { name: "删除" });
    if (await remove.isEnabled({ timeout: 15_000 }).catch(() => false)) {
      await remove.click();
      await page.waitForTimeout(2_000);
    }
  } catch {
    /* 清理是尽力而为 */
  } finally {
    await context.close();
  }
});
