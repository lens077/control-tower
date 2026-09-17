/**
 * 开发用的假后端(仅 DEV 构建、且 URL 带 `?mock` 或 sessionStorage 里有 `cc_mock` 时加载)。
 *
 * 用途:不登录 Casdoor、不连线上 config 服务,也能把整套控制台跑起来看视觉与交互。
 * 生产构建里这个模块永远不会被打进去(main.tsx 用 `import.meta.env.DEV` 守着动态 import)。
 *
 * 所有数据都是**合成的**,只为撑起界面;不要拿这里的任何数字当作线上表现。
 */
import { setTokens } from "@/auth";
import { getRuntimeConfig } from "@/runtime-config";

// ------------------------------------------------------------ 假登录

function fakeJwt(): string {
  const b64 = (s: string) => btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const header = b64(JSON.stringify({ alg: "none", typ: "JWT" }));
  const payload = b64(
    JSON.stringify({
      sub: "mock",
      name: "lens",
      displayName: "lens",
      exp: Math.floor(Date.now() / 1000) + 12 * 3600,
    }),
  );
  return `${header}.${payload}.mock`;
}

setTokens({ accessToken: fakeJwt(), refreshToken: null, idToken: null });
sessionStorage.setItem("cc_mock", "1");

// ------------------------------------------------------------ 数据

const now = Date.now();
const iso = (msAgo: number) => new Date(now - msAgo).toISOString();
const MIN = 60_000;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;

const ROUTES_YAML = `# 网关路由模板(dev)
routes:
  - name: product
    path_prefix: /api/v1/products
    target: direct://product-service.ecommerce.svc:8080
    timeout: 3s
    retries: 2
  - name: cart
    path_prefix: /api/v1/cart
    target: direct://cart-service.ecommerce.svc:8080
    timeout: 3s
    middlewares:
      - auth
      - rate-limit
  - name: order
    path_prefix: /api/v1/orders
    target: direct://order-service.ecommerce.svc:8080
    timeout: 5s
    middlewares:
      - auth
  - name: search
    path_prefix: /api/v1/search
    target: direct://search-service.ecommerce.svc:8080
    timeout: 2s
`;

const ROUTES_YAML_V4 = ROUTES_YAML.replace("timeout: 5s", "timeout: 8s");
const ROUTES_YAML_V3 = ROUTES_YAML_V4.replace(
  `  - name: search
    path_prefix: /api/v1/search
    target: direct://search-service.ecommerce.svc:8080
    timeout: 2s
`,
  "",
);
const ROUTES_YAML_V2 = ROUTES_YAML_V3.replace("direct://product-service.ecommerce.svc:8080", "discovery:///product-service");
const ROUTES_YAML_V1 = ROUTES_YAML_V2.replace(
  `    middlewares:
      - auth
      - rate-limit
`,
  "",
);

const DB_TOML = `[postgres]
host = "pg.apikv.com"
port = 30001
database = "ecommerce"
sslmode = "verify-full"
max_conns = 20
min_conns = 2

[pool]
max_conn_lifetime = "30m"
health_check_period = "1m"
`;

const FLAGS_JSON = `{
  "checkout.new_flow": true,
  "search.semantic": false,
  "cart.merge_on_login": true,
  "promo.banner": {
    "enabled": true,
    "until": "2026-10-01T00:00:00Z"
  }
}
`;

interface MockEntry {
  key: string;
  format: "CONFIG_FORMAT_YAML" | "CONFIG_FORMAT_TOML" | "CONFIG_FORMAT_JSON" | "CONFIG_FORMAT_PLAINTEXT";
  value: string;
  version: number;
  isSecret: boolean;
  description: string;
  updatedBy: string;
  updatedAgo: number;
}

const ENTRIES: Record<string, MockEntry[]> = {
  "ecommerce/dev": [
    { key: "gateway/routes.yaml", format: "CONFIG_FORMAT_YAML", value: ROUTES_YAML, version: 5, isSecret: false, description: "网关路由模板", updatedBy: "lens", updatedAgo: 2 * HOUR },
    { key: "gateway/middlewares.yaml", format: "CONFIG_FORMAT_YAML", value: "auth:\n  issuer: https://casdoor.apikv.com\nrate-limit:\n  rps: 50\n  burst: 100\n", version: 3, isSecret: false, description: "", updatedBy: "lens", updatedAgo: 3 * DAY },
    { key: "gateway/cors.yaml", format: "CONFIG_FORMAT_YAML", value: "allowed_origins:\n  - https://shop.apikv.com\n  - http://localhost:3000\n", version: 2, isSecret: false, description: "", updatedBy: "ci", updatedAgo: 9 * DAY },
    { key: "services/order/app.yaml", format: "CONFIG_FORMAT_YAML", value: "server:\n  port: 8080\nlog:\n  level: info\n", version: 7, isSecret: false, description: "", updatedBy: "lens", updatedAgo: 40 * MIN },
    { key: "services/order/db.toml", format: "CONFIG_FORMAT_TOML", value: DB_TOML, version: 4, isSecret: false, description: "订单库连接池", updatedBy: "lens", updatedAgo: 6 * DAY },
    { key: "services/product/app.yaml", format: "CONFIG_FORMAT_YAML", value: "server:\n  port: 8080\ncache:\n  ttl: 5m\n", version: 2, isSecret: false, description: "", updatedBy: "ci", updatedAgo: 12 * DAY },
    { key: "services/search/index.json", format: "CONFIG_FORMAT_JSON", value: '{\n  "shards": 3,\n  "replicas": 1\n}\n', version: 1, isSecret: false, description: "", updatedBy: "lens", updatedAgo: 30 * DAY },
    { key: "feature-flags.json", format: "CONFIG_FORMAT_JSON", value: FLAGS_JSON, version: 12, isSecret: false, description: "运行时开关", updatedBy: "lens", updatedAgo: 15 * MIN },
    { key: "rate-limit.yaml", format: "CONFIG_FORMAT_YAML", value: "default:\n  rps: 20\n", version: 1, isSecret: false, description: "", updatedBy: "ci", updatedAgo: 20 * DAY },
    { key: "smtp.secret.yaml", format: "CONFIG_FORMAT_YAML", value: "******", version: 2, isSecret: true, description: "SMTP 凭据", updatedBy: "lens", updatedAgo: 4 * DAY },
    { key: "redis.toml", format: "CONFIG_FORMAT_TOML", value: "******", version: 3, isSecret: true, description: "", updatedBy: "lens", updatedAgo: 4 * DAY },
    { key: "bootstrap.yaml", format: "CONFIG_FORMAT_YAML", value: "env: dev\nregion: ap-shanghai\n", version: 1, isSecret: false, description: "", updatedBy: "lens", updatedAgo: 45 * DAY },
    { key: "README.txt", format: "CONFIG_FORMAT_PLAINTEXT", value: "dev 环境配置。改 gateway/* 前先看 docs/design/architecture.md。\n", version: 1, isSecret: false, description: "", updatedBy: "lens", updatedAgo: 60 * DAY },
  ],
  "ecommerce/pre": [
    { key: "gateway/routes.yaml", format: "CONFIG_FORMAT_YAML", value: ROUTES_YAML, version: 2, isSecret: false, description: "", updatedBy: "operator:harvest", updatedAgo: 3 * DAY },
    { key: "services/order/app.yaml", format: "CONFIG_FORMAT_YAML", value: "server:\n  port: 8080\nlog:\n  level: warn\n", version: 3, isSecret: false, description: "", updatedBy: "lens", updatedAgo: 5 * DAY },
    { key: "feature-flags.json", format: "CONFIG_FORMAT_JSON", value: FLAGS_JSON, version: 4, isSecret: false, description: "", updatedBy: "lens", updatedAgo: 2 * DAY },
  ],
  "ecommerce/prod": [
    { key: "gateway/routes.yaml", format: "CONFIG_FORMAT_YAML", value: ROUTES_YAML_V4, version: 9, isSecret: false, description: "", updatedBy: "lens", updatedAgo: 14 * DAY },
    { key: "feature-flags.json", format: "CONFIG_FORMAT_JSON", value: FLAGS_JSON, version: 6, isSecret: false, description: "", updatedBy: "lens", updatedAgo: 10 * DAY },
  ],
  "gateway/dev": [
    { key: "listeners.yaml", format: "CONFIG_FORMAT_YAML", value: "http:\n  addr: :8080\n", version: 1, isSecret: false, description: "", updatedBy: "lens", updatedAgo: 20 * DAY },
    { key: "tls.yaml", format: "CONFIG_FORMAT_YAML", value: "******", version: 1, isSecret: true, description: "", updatedBy: "lens", updatedAgo: 20 * DAY },
  ],
  "gateway/pre": [
    { key: "listeners.yaml", format: "CONFIG_FORMAT_YAML", value: "http:\n  addr: :8080\n", version: 1, isSecret: false, description: "", updatedBy: "lens", updatedAgo: 20 * DAY },
  ],
  "payment/dev": [
    { key: "providers.yaml", format: "CONFIG_FORMAT_YAML", value: "alipay:\n  enabled: true\nwechat:\n  enabled: true\n", version: 3, isSecret: false, description: "", updatedBy: "lens", updatedAgo: 8 * DAY },
    { key: "keys.secret.yaml", format: "CONFIG_FORMAT_YAML", value: "******", version: 2, isSecret: true, description: "", updatedBy: "lens", updatedAgo: 8 * DAY },
  ],
  "payment/prod": [
    { key: "providers.yaml", format: "CONFIG_FORMAT_YAML", value: "alipay:\n  enabled: true\n", version: 5, isSecret: false, description: "", updatedBy: "lens", updatedAgo: 30 * DAY },
  ],
};

function entryJson(ns: string, env: string, e: MockEntry, withValue: boolean) {
  return {
    id: "1",
    namespace: ns,
    environment: env,
    key: e.key,
    format: e.format,
    value: withValue ? e.value : "",
    version: e.version,
    isSecret: e.isSecret,
    description: e.description,
    updatedBy: e.updatedBy,
    createdAt: iso(e.updatedAgo + 40 * DAY),
    updatedAt: iso(e.updatedAgo),
  };
}

function listNamespaces() {
  const map = new Map<string, { environments: Set<string>; keyCount: number }>();
  for (const [loc, entries] of Object.entries(ENTRIES)) {
    const [ns, env] = loc.split("/");
    const info = map.get(ns) ?? { environments: new Set<string>(), keyCount: 0 };
    info.environments.add(env);
    info.keyCount += entries.length;
    map.set(ns, info);
  }
  return {
    namespaces: [...map.entries()]
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([namespace, info]) => ({
        namespace,
        environments: [...info.environments].sort(),
        keyCount: info.keyCount,
      })),
  };
}

function revisions(ns: string, env: string, key: string) {
  const entry = ENTRIES[`${ns}/${env}`]?.find((e) => e.key === key);
  if (!entry) return { revisions: [] };
  if (key === "gateway/routes.yaml" && ns === "ecommerce" && env === "dev") {
    return {
      revisions: [
        { id: "5", entryId: "1", version: 5, format: "CONFIG_FORMAT_YAML", value: ROUTES_YAML, comment: "search 路由加回来,超时 2s", author: "lens", createdAt: iso(2 * HOUR) },
        { id: "4", entryId: "1", version: 4, format: "CONFIG_FORMAT_YAML", value: ROUTES_YAML_V4, comment: "order 超时放宽到 8s(大促压测)", author: "lens", createdAt: iso(1 * DAY) },
        { id: "3", entryId: "1", version: 3, format: "CONFIG_FORMAT_YAML", value: ROUTES_YAML_V3, comment: "", author: "ci", createdAt: iso(6 * DAY) },
        { id: "2", entryId: "1", version: 2, format: "CONFIG_FORMAT_YAML", value: ROUTES_YAML_V2, comment: "cart 加 auth + rate-limit", author: "lens", createdAt: iso(12 * DAY) },
        { id: "1", entryId: "1", version: 1, format: "CONFIG_FORMAT_YAML", value: ROUTES_YAML_V1, comment: "从 ecommerce 仓迁入", author: "lens", createdAt: iso(40 * DAY) },
      ],
    };
  }
  const revs = [];
  for (let v = entry.version; v >= 1; v -= 1) {
    revs.push({
      id: String(v),
      entryId: "1",
      version: v,
      format: entry.format,
      value: entry.isSecret ? "******" : v === entry.version ? entry.value : `${entry.value}# v${v}\n`,
      comment: v === entry.version ? "" : `调整 v${v}`,
      author: v % 2 ? "lens" : "ci",
      createdAt: iso(entry.updatedAgo + (entry.version - v) * 3 * DAY),
    });
  }
  return { revisions: revs };
}

const CONNECTIONS = {
  connections: [
    {
      clientName: "control-tower-gateway",
      clientInstance: "gateway-7d9c6b7c9f-x2kqr",
      clientVersion: "0.2.10",
      targets: [
        { namespace: "ecommerce", environment: "dev", key: "gateway/routes.yaml" },
        { namespace: "ecommerce", environment: "dev", key: "gateway/middlewares.yaml" },
        { namespace: "ecommerce", environment: "dev", key: "gateway/cors.yaml" },
      ],
      watching: true,
      connectedAt: iso(5 * HOUR),
      lastReadAt: iso(2 * HOUR),
      lastWatchAt: iso(12_000),
    },
    {
      clientName: "control-tower-gateway",
      clientInstance: "gateway-7d9c6b7c9f-m8vzd",
      clientVersion: "0.2.10",
      targets: [
        { namespace: "ecommerce", environment: "dev", key: "gateway/routes.yaml" },
        { namespace: "ecommerce", environment: "dev", key: "gateway/middlewares.yaml" },
      ],
      watching: true,
      connectedAt: iso(5 * HOUR),
      lastReadAt: iso(2 * HOUR),
      lastWatchAt: iso(9_000),
    },
    {
      clientName: "order-service",
      clientInstance: "order-5f6d8b9c4-qq2p7",
      clientVersion: "1.14.2",
      targets: [
        { namespace: "ecommerce", environment: "dev", key: "services/order/app.yaml" },
        { namespace: "ecommerce", environment: "dev", key: "services/order/db.toml" },
      ],
      watching: false,
      connectedAt: iso(3 * DAY),
      lastReadAt: iso(26 * MIN),
      lastWatchAt: iso(26 * MIN),
      disconnectedAt: iso(25 * MIN),
      lastDisconnectReason: "client closed stream (rolling update)",
    },
  ],
};

interface MockToken {
  id: string;
  serviceName: string;
  environment: string;
  allowedNamespaces: string[];
  note: string;
  disabled: boolean;
  createdAt: string;
  revokedAt?: string;
  lastUsedAt?: string;
  role: string;
}

const TOKENS: { tokens: MockToken[] } = {
  tokens: [
    { id: "mt_01", serviceName: "control-tower-gateway", environment: "dev", allowedNamespaces: ["ecommerce", "gateway"], note: "网关 dev 副本", disabled: false, createdAt: iso(18 * DAY), lastUsedAt: iso(12_000), role: "MACHINE_TOKEN_ROLE_SERVICE" },
    { id: "mt_02", serviceName: "order-service", environment: "dev", allowedNamespaces: [], note: "", disabled: false, createdAt: iso(18 * DAY), lastUsedAt: iso(26 * MIN), role: "MACHINE_TOKEN_ROLE_SERVICE" },
    { id: "mt_03", serviceName: "harvest", environment: "pre", allowedNamespaces: ["ecommerce"], note: "operator:harvest,configctl 写 pre 键", disabled: false, createdAt: iso(3 * DAY), lastUsedAt: iso(3 * DAY), role: "MACHINE_TOKEN_ROLE_OPERATOR" },
    { id: "mt_04", serviceName: "search-service", environment: "dev", allowedNamespaces: ["ecommerce"], note: "已换成 scoped token", disabled: true, createdAt: iso(60 * DAY), revokedAt: iso(20 * DAY), lastUsedAt: iso(21 * DAY), role: "MACHINE_TOKEN_ROLE_SERVICE" },
  ],
};

const STATUS = {
  process: {
    cpuPercent: 3.8,
    cpuLimitCores: 1,
    memoryRssBytes: String(118 * 1024 * 1024),
    memoryLimitBytes: String(512 * 1024 * 1024),
    goHeapBytes: String(64 * 1024 * 1024),
    goroutines: 84,
    gcCount: 1203,
    diskPath: "/",
    diskUsedBytes: String(Math.round(21.4 * 1024 * 1024 * 1024)),
    diskTotalBytes: String(60 * 1024 * 1024 * 1024),
    netRxBytesPerSec: 18_400,
    netTxBytesPerSec: 42_100,
    limitsFromCgroup: true,
    sampledAt: iso(0),
    uptime: "1123200s",
    degraded: [],
  },
  dependencies: [
    { name: "postgres", healthy: true, detail: "pg.apikv.com:30001 · 2 ms" },
    { name: "redis", healthy: true, detail: "redis.apikv.com:30002 · 1 ms" },
    { name: "victoriametrics", healthy: true, detail: "http://metrics.apikv.com" },
  ],
  build: {
    serviceName: "config-center",
    version: "0.2.11",
    environment: "pre",
    goVersion: "go1.25.1",
    buildVersion: "sha-8f1c2d0",
  },
  metricsBackendAvailable: true,
};

const SERIES_UNIT: Record<string, { unit: string; lines: Array<{ label: string; base: number; amp: number }> }> = {
  METRIC_SERIES_PROCESS_CPU: { unit: "METRIC_UNIT_PERCENT", lines: [{ label: "cpu", base: 4, amp: 2.5 }] },
  METRIC_SERIES_PROCESS_MEMORY: { unit: "METRIC_UNIT_BYTES", lines: [{ label: "rss", base: 118e6, amp: 6e6 }, { label: "heap", base: 64e6, amp: 12e6 }] },
  METRIC_SERIES_PROCESS_GOROUTINES: { unit: "METRIC_UNIT_COUNT", lines: [{ label: "goroutines", base: 84, amp: 10 }] },
  METRIC_SERIES_PROCESS_NETWORK: { unit: "METRIC_UNIT_BYTES_PER_SECOND", lines: [{ label: "rx", base: 18e3, amp: 9e3 }, { label: "tx", base: 42e3, amp: 15e3 }] },
  METRIC_SERIES_HOST_CPU: { unit: "METRIC_UNIT_PERCENT", lines: [{ label: "node3", base: 31, amp: 12 }] },
  METRIC_SERIES_HOST_MEMORY: { unit: "METRIC_UNIT_PERCENT", lines: [{ label: "node3", base: 62, amp: 3 }] },
  METRIC_SERIES_HOST_DISK: { unit: "METRIC_UNIT_PERCENT", lines: [{ label: "/", base: 35.6, amp: 0.2 }] },
  METRIC_SERIES_HOST_NETWORK: { unit: "METRIC_UNIT_BYTES_PER_SECOND", lines: [{ label: "rx", base: 1.2e6, amp: 6e5 }, { label: "tx", base: 8e5, amp: 3e5 }] },
  METRIC_SERIES_API_LATENCY: { unit: "METRIC_UNIT_MILLISECONDS", lines: [{ label: "p50", base: 4, amp: 1 }, { label: "p90", base: 11, amp: 4 }, { label: "p99", base: 38, amp: 18 }] },
  METRIC_SERIES_API_THROUGHPUT: { unit: "METRIC_UNIT_REQUESTS_PER_SECOND", lines: [{ label: "GetKey", base: 12, amp: 6 }, { label: "WatchKeys", base: 1.5, amp: 0.8 }, { label: "ListKeys", base: 0.6, amp: 0.5 }] },
  METRIC_SERIES_API_ERROR_RATE: { unit: "METRIC_UNIT_PERCENT", lines: [{ label: "5xx", base: 0.05, amp: 0.05 }] },
  METRIC_SERIES_DB_LATENCY: { unit: "METRIC_UNIT_MILLISECONDS", lines: [{ label: "p50", base: 1.2, amp: 0.4 }, { label: "p99", base: 6, amp: 3 }] },
  METRIC_SERIES_DB_POOL: { unit: "METRIC_UNIT_COUNT", lines: [{ label: "acquired", base: 3, amp: 2 }, { label: "idle", base: 5, amp: 2 }] },
};

function queryMetrics(body: { series: string[]; window?: string; stepSeconds?: number }) {
  const windowSec = Number(String(body.window ?? "3600s").replace("s", ""));
  const step = body.stepSeconds ?? 30;
  const n = Math.min(400, Math.floor(windowSec / step));
  const results = body.series.map((series) => {
    const def = SERIES_UNIT[series];
    if (!def) return { series, lines: [], error: "" };
    return {
      series,
      error: "",
      lines: def.lines.map((line, li) => ({
        label: line.label,
        unit: def.unit,
        points: Array.from({ length: n }, (_, i) => {
          const t = now - (n - i) * step * 1000;
          const phase = (i / n) * Math.PI * 2 * (2 + li);
          const noise = Math.sin(i * 7.3 + li) * 0.3 + Math.sin(i * 1.7) * 0.2;
          return { tsMs: String(t), value: Math.max(0, line.base + line.amp * (Math.sin(phase) + noise)) };
        }),
      })),
    };
  });
  return { results, metricsBackendAvailable: true };
}

// ------------------------------------------------------------ 拦截

const apiUrl = getRuntimeConfig().apiUrl.replace(/\/$/, "");
const realFetch = window.fetch.bind(window);

function json(body: unknown, status = 200, headers: Record<string, string> = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json", ...headers },
  });
}

function connectError(code: string, message: string, status: number) {
  return json({ code, message }, status);
}

function decodeBody(raw: BodyInit | null | undefined): Record<string, any> {
  if (!raw) return {};
  let text: string;
  if (typeof raw === "string") text = raw;
  else if (raw instanceof Uint8Array) text = new TextDecoder().decode(raw);
  else if (raw instanceof ArrayBuffer) text = new TextDecoder().decode(new Uint8Array(raw));
  else return {};
  try {
    return text ? (JSON.parse(text) as Record<string, any>) : {};
  } catch {
    return {};
  }
}

window.fetch = async (input, init) => {
  const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
  if (!url.startsWith(apiUrl)) return realFetch(input, init);
  const method = url.slice(apiUrl.length);
  const body = decodeBody(init?.body);
  await new Promise((r) => setTimeout(r, 120 + Math.random() * 180));

  switch (method) {
    case "/config.v1.ConfigService/ListNamespaces":
      return json(listNamespaces());
    case "/config.v1.ConfigService/ListKeys": {
      const entries = (ENTRIES[`${body.namespace}/${body.environment}`] ?? [])
        .filter((e) => !body.keyPrefix || e.key.startsWith(body.keyPrefix))
        .map((e) => entryJson(body.namespace, body.environment, e, false));
      return json({ entries });
    }
    case "/config.v1.ConfigService/GetKey": {
      const e = ENTRIES[`${body.namespace}/${body.environment}`]?.find((x) => x.key === body.key);
      if (!e) return connectError("not_found", `key ${body.key} not found`, 404);
      return json({ entry: entryJson(body.namespace, body.environment, e, true) });
    }
    case "/config.v1.ConfigService/PutKey": {
      const loc = `${body.namespace}/${body.environment}`;
      const list = (ENTRIES[loc] ??= []);
      let e = list.find((x) => x.key === body.key);
      if (!e) {
        e = { key: body.key, format: body.format, value: body.value, version: 0, isSecret: !!body.isSecret, description: body.description ?? "", updatedBy: "lens", updatedAgo: 0 };
        list.push(e);
      }
      e.version += 1;
      e.value = body.value;
      e.format = body.format;
      e.isSecret = !!body.isSecret;
      e.description = body.description ?? "";
      e.updatedAgo = 0;
      return json({ entry: entryJson(body.namespace, body.environment, e, true) });
    }
    case "/config.v1.ConfigService/DeleteKey": {
      const loc = `${body.namespace}/${body.environment}`;
      ENTRIES[loc] = (ENTRIES[loc] ?? []).filter((x) => x.key !== body.key);
      if (ENTRIES[loc].length === 0) delete ENTRIES[loc];
      return json({});
    }
    case "/config.v1.ConfigService/ListRevisions":
      return json(revisions(body.namespace, body.environment, body.key));
    case "/config.v1.ConfigService/Rollback": {
      const e = ENTRIES[`${body.namespace}/${body.environment}`]?.find((x) => x.key === body.key);
      if (e) e.version += 1;
      return json({ entry: e ? entryJson(body.namespace, body.environment, e, true) : undefined });
    }
    case "/config.v1.ConfigService/ListClientConnections":
      return json(CONNECTIONS, 200, { "x-config-center-presence-mode": "redis_ttl" });
    case "/config.v1.ConfigService/ListMachineTokens":
      return json({
        tokens: TOKENS.tokens.filter(
          (tk) =>
            (!body.serviceName || tk.serviceName.includes(body.serviceName)) &&
            (!body.environment || tk.environment === body.environment),
        ),
      });
    case "/config.v1.ConfigService/IssueMachineToken": {
      const meta: MockToken = { id: `mt_${Date.now()}`, serviceName: body.serviceName, environment: body.environment, allowedNamespaces: body.allowedNamespaces ?? [], note: body.note ?? "", disabled: false, createdAt: iso(0), role: body.role ?? "MACHINE_TOKEN_ROLE_SERVICE" };
      TOKENS.tokens.unshift(meta);
      return json({ meta, token: "ct_mock_" + Math.random().toString(36).slice(2) + Math.random().toString(36).slice(2) });
    }
    case "/config.v1.ConfigService/RevokeMachineToken": {
      const tk = TOKENS.tokens.find((x) => x.id === body.id);
      if (tk) {
        tk.disabled = true;
        tk.revokedAt = iso(0);
      }
      return json({});
    }
    case "/system.v1.SystemService/GetSystemStatus":
      return json(STATUS);
    case "/system.v1.SystemService/QueryMetrics":
      return json(queryMetrics(body as { series: string[] }));
    default:
      return connectError("unimplemented", `mock: ${method}`, 501);
  }
};

console.info("[mock] 假后端已启用:数据全部为合成,仅用于查看界面");
