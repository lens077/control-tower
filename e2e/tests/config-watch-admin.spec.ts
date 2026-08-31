import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { expect, test, type Page } from "@playwright/test";
import {
  ConfigService,
  WatchEventType,
  type WatchKeysResponse,
} from "../../web/src/gen/api/config/v1/config_pb";
import {
  CONFIG_API_URL,
  STORAGE_STATE,
} from "../playwright.config";

const ADMIN_MUTATIONS_ENABLED = process.env.E2E_ADMIN_MUTATIONS === "true";
const RUN_ID = Date.now().toString(36);
const NS = "e2e";
const ENV = "dev";
const KEY = `watch-${RUN_ID}.yaml`;
const SERVICE_NAME = `e2e-watch-${RUN_ID}`;
const CLIENT_NAME = "e2e-watch-client";
const CLIENT_INSTANCE = `playwright-${RUN_ID}`;
const EDIT_URL = `/edit?ns=${NS}&env=${ENV}&key=${KEY}`;
const INITIAL_VALUE = "watch: initial";
const UPDATED_VALUE = "watch: updated";

const client = createClient(
  ConfigService,
  createConnectTransport({
    baseUrl: CONFIG_API_URL,
    useBinaryFormat: true,
  }),
);

let issuedToken = "";
let watchAbort: AbortController | undefined;
let watchTask: Promise<void> | undefined;
let watchDone = false;
let watchError: unknown;
const watchEvents: WatchKeysResponse[] = [];

function versionChip(page: Page) {
  return page.locator(".MuiChip-label").filter({ hasText: /^(v\d+|新建)$/ });
}

async function expectSignedIn(page: Page) {
  await expect(page.getByRole("button", { name: "登出" })).toBeVisible({ timeout: 30_000 });
}

async function waitForEditorContent(page: Page, expected: string) {
  await expect(page.locator(".view-line").first()).toHaveText(expected, { timeout: 45_000 });
}

async function replaceEditorContent(page: Page, text: string) {
  await page.evaluate((value) => {
    const monaco = (window as unknown as { monaco?: any }).monaco;
    const model = monaco?.editor?.getModels?.()?.[0];
    if (!model) throw new Error("拿不到 Monaco model —— 编辑器没有挂载成功");
    model.setValue(value);
  }, text);
  await waitForEditorContent(page, text);
  await expect(page.getByRole("button", { name: "保存" })).toBeEnabled({ timeout: 15_000 });
}

async function saveAndExpectVersion(page: Page, version: string) {
  await page.getByRole("button", { name: "保存" }).click();
  await expect(page.locator(".MuiAlert-standardError")).toHaveCount(0, { timeout: 15_000 });
  await expect(versionChip(page)).toHaveText(version, { timeout: 30_000 });
}

async function createWatchKey(page: Page) {
  await page.goto(EDIT_URL);
  await expectSignedIn(page);
  await expect(versionChip(page)).toHaveText("新建", { timeout: 30_000 });
  await expect(page.locator(".monaco-editor")).toBeVisible({ timeout: 45_000 });
  await replaceEditorContent(page, INITIAL_VALUE);
  await saveAndExpectVersion(page, "v1");
}

async function updateWatchKey(page: Page) {
  await page.goto(EDIT_URL);
  await expectSignedIn(page);
  await waitForEditorContent(page, INITIAL_VALUE);
  await replaceEditorContent(page, UPDATED_VALUE);
  await saveAndExpectVersion(page, "v2");
}

async function issueMachineToken(page: Page): Promise<string> {
  await page.goto("/tokens");
  await expectSignedIn(page);
  await page.getByRole("button", { name: "签发 Token" }).click();

  const issueDialog = page.getByRole("dialog").filter({ hasText: "签发 Machine Token" });
  await issueDialog.getByLabel("服务名").fill(SERVICE_NAME);
  await issueDialog.getByLabel("环境").fill(ENV);
  await issueDialog.getByLabel("允许的命名空间").fill(NS);
  await issueDialog.getByLabel("备注").fill(`WatchKeys / connections / revoke e2e ${RUN_ID}`);
  await issueDialog.getByRole("button", { name: "签发", exact: true }).click();

  const issuedDialog = page.getByRole("dialog").filter({ hasText: "Machine Token 已签发" });
  const token = (await issuedDialog.getByTestId("issued-token").textContent())?.trim() ?? "";
  if (!token.startsWith("ct_") || token.length < 40) {
    throw new Error("签发响应没有返回符合格式的 Machine Token");
  }
  // 先记录，后关对话框；即使 UI 操作随后失败，afterAll 仍能定位并吊销已签发的 token。
  issuedToken = token;
  await issuedDialog.getByRole("button", { name: "我已复制，关闭" }).click();
  return token;
}

function machineHeaders(token: string): Headers {
  return new Headers({
    "x-config-center-service-token": token,
    "x-config-center-client-name": CLIENT_NAME,
    "x-config-center-client-instance": CLIENT_INSTANCE,
    "x-config-center-client-version": "playwright-e2e",
  });
}

function startWatch(token: string) {
  watchAbort = new AbortController();
  watchTask = (async () => {
    try {
      for await (const event of client.watchKeys(
        { namespace: NS, environment: ENV, keys: [KEY] },
        { headers: machineHeaders(token), signal: watchAbort.signal },
      )) {
        watchEvents.push(event);
      }
    } catch (error) {
      if (!watchAbort.signal.aborted) watchError = error;
    } finally {
      watchDone = true;
    }
  })();
}

function sawEvent(type: WatchEventType, value: string): boolean {
  return watchEvents.some((event) => (
    event.type === type
    && event.entry?.key === KEY
    && event.entry.value === value
  ));
}

function connectionCard(page: Page) {
  return page.locator(".MuiCard-root").filter({
    has: page.getByText(CLIENT_NAME, { exact: true }),
  });
}

function tokenCard(page: Page) {
  return page.locator(".MuiCard-root").filter({
    has: page.getByText(SERVICE_NAME, { exact: true }),
  });
}

async function revokeIssuedToken(page: Page) {
  await page.goto("/tokens");
  await expectSignedIn(page);
  await page.getByLabel("服务名").fill(SERVICE_NAME);
  const card = tokenCard(page);
  await expect(card).toBeVisible({ timeout: 30_000 });
  await card.getByRole("button", { name: "吊销" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "确认吊销" }).click();
  await expect(card).toContainText("已吊销", { timeout: 30_000 });
}

async function deleteWatchKey(page: Page) {
  await page.goto(EDIT_URL, { waitUntil: "domcontentloaded" });
  const remove = page.getByRole("button", { name: "删除" });
  if (await remove.isEnabled({ timeout: 15_000 }).catch(() => false)) {
    await remove.click();
    await page.waitForURL(/\/$|\/\?/, { timeout: 30_000 });
  }
}

test.describe("真实 WatchKeys、客户端连接与 token 吊销", () => {
  test.describe.configure({ mode: "serial" });
  test.skip(
    !ADMIN_MUTATIONS_ENABLED,
    "会签发并吊销 Machine Token；仅在发布后显式设置 E2E_ADMIN_MUTATIONS=true 时运行",
  );

  test("WatchKeys 长流收到真实 SNAPSHOT 与 PUT", async ({ page }) => {
    await createWatchKey(page);
    issuedToken = await issueMachineToken(page);
    startWatch(issuedToken);

    await expect.poll(
      () => sawEvent(WatchEventType.SNAPSHOT, INITIAL_VALUE),
      { timeout: 30_000, message: "WatchKeys 没有下发初始快照" },
    ).toBe(true);

    await updateWatchKey(page);
    await expect.poll(
      () => sawEvent(WatchEventType.PUT, UPDATED_VALUE),
      { timeout: 30_000, message: "WatchKeys 没有下发配置更新" },
    ).toBe(true);
    expect(watchDone).toBe(false);
    expect(watchError).toBeUndefined();
  });

  test("/connections 显示真实订阅客户端和目标 key", async ({ page }) => {
    expect(watchDone).toBe(false);
    await page.goto("/connections");
    await expectSignedIn(page);

    const card = connectionCard(page);
    await expect(card).toBeVisible({ timeout: 30_000 });
    await expect(card).toContainText(CLIENT_INSTANCE);
    await expect(card).toContainText("订阅中");
    await expect(card).toContainText(`${NS}/${ENV}/${KEY}`);
  });

  test("吊销 token 会终止长流并拒绝后续读取", async ({ page }) => {
    test.slow();
    await revokeIssuedToken(page);

    await expect.poll(
      () => watchDone,
      { timeout: 45_000, message: "token 吊销后 WatchKeys 没有在心跳周期内断开" },
    ).toBe(true);
    await watchTask;

    const streamError = ConnectError.from(watchError);
    expect(streamError.code).toBe(Code.PermissionDenied);

    let readError: unknown;
    try {
      await client.getKey(
        { namespace: NS, environment: ENV, key: KEY },
        { headers: machineHeaders(issuedToken) },
      );
    } catch (error) {
      readError = error;
    }
    expect(ConnectError.from(readError).code).toBe(Code.Unauthenticated);

    await page.goto("/connections");
    const card = connectionCard(page);
    await expect(card).toBeVisible({ timeout: 30_000 });
    await expect(card).toContainText("已断开");
    await expect(card).toContainText("token_revoked");
  });

  test.afterAll(async ({ browser }) => {
    watchAbort?.abort();
    await watchTask?.catch(() => undefined);

    const context = await browser.newContext({ storageState: STORAGE_STATE });
    const page = await context.newPage();
    try {
      // 即使签发响应在返回明文前就断了，也按唯一 service name 查卡片并补做吊销。
      await page.goto("/tokens");
      await page.getByLabel("服务名").fill(SERVICE_NAME);
      const revoke = tokenCard(page).getByRole("button", { name: "吊销" });
      if (await revoke.isEnabled({ timeout: 15_000 }).catch(() => false)) {
        await revoke.click();
        await page.getByRole("dialog").getByRole("button", { name: "确认吊销" }).click();
      }
    } finally {
      await deleteWatchKey(page).catch(() => undefined);
      await context.close();
    }
  });
});
