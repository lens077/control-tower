import { defineConfig, devices } from "@playwright/test";

/**
 * 打的是**真实环境**，不是本地起的 mock：这套用例的价值就在于覆盖只有真环境才会
 * 暴露的东西 —— CSP 响应头、Pangolin 隧道、Casdoor 单点登录、node3 上的 PG/Redis/VM。
 * 因此没有 webServer 段，地址一律由环境变量给。
 */
export const CONFIG_URL = process.env.E2E_CONFIG_URL ?? "https://config.apikv.com";
export const GATEWAY_URL = process.env.E2E_GATEWAY_URL ?? "https://gateway.apikv.com";

/** 登录态存这里。凭据只从环境变量来，绝不写进仓库（见 AGENTS.md 硬约束 4）。 */
export const STORAGE_STATE = "./.auth/state.json";

export default defineConfig({
  testDir: "./tests",
  // 打公网，单条用例给足时间；失败重试一次以过滤隧道抖动。
  timeout: 90_000,
  expect: { timeout: 20_000 },
  retries: process.env.CI ? 1 : 0,
  // 串行:用例之间共享同一个 Casdoor 会话，并发登录会互相踢。
  workers: 1,
  fullyParallel: false,
  globalSetup: "./global-setup.ts",
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: CONFIG_URL,
    trace: "retain-on-failure",
    video: "retain-on-failure",
    screenshot: "only-on-failure",
    ignoreHTTPSErrors: false,
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"], storageState: STORAGE_STATE },
    },
  ],
});
