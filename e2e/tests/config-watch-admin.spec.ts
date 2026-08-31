import { expect, test, type Page } from "@playwright/test";
import { CONFIG_API_URL, STORAGE_STATE } from "../playwright.config";

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
const WATCH_SNAPSHOT = "WATCH_EVENT_TYPE_SNAPSHOT";
const WATCH_PUT = "WATCH_EVENT_TYPE_PUT";
const END_STREAM_FLAG = 0x02;

interface WatchResponse {
  type: string | number;
  entry?: { key?: string; value?: string };
}

interface EndStreamResponse {
  error?: { code?: string };
}

let issuedToken = "";
let watchAbort: AbortController | undefined;
let watchTask: Promise<void> | undefined;
let watchDone = false;
let watchError: unknown;
let watchEndCode = "";
const watchEvents: WatchResponse[] = [];

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

function machineHeaders(token: string): Record<string, string> {
  return {
    "x-config-center-service-token": token,
    "x-config-center-client-name": CLIENT_NAME,
    "x-config-center-client-instance": CLIENT_INSTANCE,
    "x-config-center-client-version": "playwright-e2e",
  };
}

function encodeEnvelope(message: unknown): Uint8Array {
  const payload = new TextEncoder().encode(JSON.stringify(message));
  const envelope = new Uint8Array(5 + payload.length);
  new DataView(envelope.buffer).setUint32(1, payload.length, false);
  envelope.set(payload, 5);
  return envelope;
}

function appendBytes(left: Uint8Array, right: Uint8Array): Uint8Array {
  const combined = new Uint8Array(left.length + right.length);
  combined.set(left);
  combined.set(right, left.length);
  return combined;
}

// Connect 的 JSON 服务端流同样使用 5 字节 envelope。解析器放在测试侧，
// 避免跨 package 根目录导入 Web 生成代码，导致 CI 找不到它的运行时依赖。
async function consumeWatchBody(stream: ReadableStream<Uint8Array>) {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let pending = new Uint8Array();

  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    pending = appendBytes(pending, value);

    while (pending.length >= 5) {
      const length = new DataView(pending.buffer, pending.byteOffset + 1, 4).getUint32(0, false);
      if (pending.length < 5 + length) break;

      const flags = pending[0];
      const payload = pending.slice(5, 5 + length);
      pending = pending.slice(5 + length);
      if (flags === END_STREAM_FLAG) {
        const end = JSON.parse(decoder.decode(payload)) as EndStreamResponse;
        watchEndCode = end.error?.code ?? "";
        return;
      }
      if (flags !== 0) throw new Error(`WatchKeys 返回不支持的 Connect envelope flags: ${flags}`);
      watchEvents.push(JSON.parse(decoder.decode(payload)) as WatchResponse);
    }
  }

  if (pending.length !== 0) throw new Error("WatchKeys 返回了截断的 Connect envelope");
}

async function startWatch(token: string) {
  const abort = new AbortController();
  watchAbort = abort;
  const response = await fetch(`${CONFIG_API_URL}/config.v1.ConfigService/WatchKeys`, {
    method: "POST",
    headers: {
      ...machineHeaders(token),
      "connect-protocol-version": "1",
      "content-type": "application/connect+json",
    },
    body: encodeEnvelope({ namespace: NS, environment: ENV, keys: [KEY] }),
    signal: abort.signal,
  });
  if (!response.ok || !response.body) {
    throw new Error(`WatchKeys 建流失败，HTTP ${response.status}`);
  }

  watchTask = consumeWatchBody(response.body)
    .catch((error) => {
      if (!abort.signal.aborted) watchError = error;
    })
    .finally(() => {
      watchDone = true;
    });
}

async function getKeyStatus(token: string): Promise<number> {
  const response = await fetch(`${CONFIG_API_URL}/config.v1.ConfigService/GetKey`, {
    method: "POST",
    headers: {
      ...machineHeaders(token),
      "connect-protocol-version": "1",
      "content-type": "application/json",
    },
    body: JSON.stringify({ namespace: NS, environment: ENV, key: KEY }),
  });
  await response.arrayBuffer();
  return response.status;
}

function sawEvent(type: string, numericType: number, value: string): boolean {
  return watchEvents.some((event) => (
    (event.type === type || event.type === numericType)
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
    await startWatch(issuedToken);

    await expect.poll(
      () => sawEvent(WATCH_SNAPSHOT, 1, INITIAL_VALUE),
      { timeout: 30_000, message: "WatchKeys 没有下发初始快照" },
    ).toBe(true);

    await updateWatchKey(page);
    await expect.poll(
      () => sawEvent(WATCH_PUT, 2, UPDATED_VALUE),
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

    expect(watchError).toBeUndefined();
    expect(watchEndCode).toBe("permission_denied");
    expect(await getKeyStatus(issuedToken)).toBe(401);

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
