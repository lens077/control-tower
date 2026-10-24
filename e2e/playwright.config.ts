import { defineConfig, devices } from "@playwright/test";

/**
 * 打的是**真实环境**，不是本地起的 mock：这套用例的价值就在于覆盖只有真环境才会
 * 暴露的东西 —— CSP 响应头、Casdoor 单点登录、Pangolin 隧道与 EdgeOne Pages 公网链路、
 * node3 上的 PG/Redis/VM。
 * 因此没有 webServer 段，地址一律由环境变量给。
 *
 * 控制台默认打 **e2e 专属域** e2e.control-tower.apikv.com，不是生产域
 * control-tower.apikv.com。两者指向同一份 EdgeOne Makers 部署（同一份 dist、同一套
 * Casdoor 与后端），只是 Host 不同 —— 这样一天 4 轮的巡检流量不会混进生产域的
 * EdgeOne 流量统计，看板里按域名筛就能只看真实用户。
 * 要验证生产域自身的链路（DNS / 证书 / Casdoor 生产回调）就显式传
 * E2E_CONFIG_URL=https://control-tower.apikv.com；要打 k8s 控制台传
 * E2E_CONFIG_URL=https://config.apikv.com。
 */
/**
 * 取环境变量，**空串等同于没给**。
 *
 * `??` 在这里不够用：GitHub Actions 里 `${{ inputs.x }}` 在 schedule 触发时会展开成
 * 空串而不是不存在，`process.env.X ?? 默认值` 会得到 `""`，baseURL 就成了空 —— 表现为
 * 所有用例莫名其妙地打到 about:blank。
 */
function envOr(name: string, fallback: string): string {
  const value = process.env[name]?.trim();
  return value ? value : fallback;
}

/**
 * e2e 的 User-Agent 标记。
 *
 * 这套用例打的是真实公网环境，于是它的请求会被 EdgeOne Makers 的流量统计当成真实用户
 * 访问：一天 4 轮，每轮整页加载要拉全量静态资源（含 12 个 woff2 与 Monaco 的 /vs），
 * 量级足以把一个低频站点看板冲掉。在 UA 尾部追加专有标记后，就能在 EdgeOne
 * 「数据分析 → 指标分析」里按 User-Agent 把这部分排除掉，只看真实用户。
 *
 * 为什么是 UA 而不是自定义请求头：指标分析的筛选项只有 UA / Referer / 客户端 IP 这些
 * 标准维度，没有自定义头。为什么不是按 IP：GitHub Actions 托管 runner 的出口 IP 有
 * 7081 个 CIDR（`curl https://api.github.com/meta` 的 actions 字段），没法维护。
 *
 * ⚠️ 改这里要同步改 `.github/workflows/e2e.yml` 里 preflight curl 的 `-A` 参数，
 * 否则预检那一次请求不会被标记。
 */
export const E2E_UA_MARKER = "control-tower-e2e/1.0";

/**
 * 完整 UA = 桌面 Chrome 的 UA + 标记。
 * 保留 Chrome 前缀是为了不触发任何基于 UA 的降级分支（Casdoor / Monaco / 控制台都没有，
 * 但没必要冒这个险）；标记放尾部，服务端与 CDN 都按前缀识别，不受影响。
 */
const CHROME_UA =
  devices["Desktop Chrome"].userAgent ??
  "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36";
export const E2E_USER_AGENT = `${CHROME_UA} ${E2E_UA_MARKER}`.trim();

export const CONFIG_URL = envOr("E2E_CONFIG_URL", "https://e2e.control-tower.apikv.com");
export const CONFIG_API_URL = envOr("E2E_CONFIG_API_URL", "https://config-api.apikv.com");
export const GATEWAY_URL = envOr("E2E_GATEWAY_URL", "https://gateway.apikv.com");
export const METRICS_URL = envOr("E2E_METRICS_URL", "https://metrics.apikv.com");
/**
 * metrics.apikv.com 经 vmauth 只读（2026-09-29 起），不带只读 token 返回 401。
 * 取值：kubectl -n victoriametrics get secret vmauth-credentials -o jsonpath='{.data.read-token}' | base64 -d
 * CI 用仓库 Secret E2E_METRICS_TOKEN。
 */
export const METRICS_TOKEN = envOr("E2E_METRICS_TOKEN", "");

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
    // goto/reload 的兜底超时：默认 30s 在隧道抖动时太紧。45s 给足首屏抵达时间，
    // 真正的重试逻辑在 nav.ts 的 navigate() 里（domcontentloaded + 重试）。
    navigationTimeout: 45_000,
    trace: "retain-on-failure",
    video: "retain-on-failure",
    screenshot: "only-on-failure",
    ignoreHTTPSErrors: false,
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"], userAgent: E2E_USER_AGENT, storageState: STORAGE_STATE },
    },
  ],
});
