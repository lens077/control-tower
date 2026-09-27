/// <reference types="vite-plus" />
import { defineConfig } from "vite-plus";
import { playwright } from "vite-plus/test/browser-playwright"; // 浏览器测试 provider
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import { createReadStream } from "node:fs";
import { cp } from "node:fs/promises";
import { createRequire } from "node:module";
import { connect, createServer as createNetServer } from "node:net";
import { extname, join, normalize, resolve } from "node:path";

/**
 * monaco 资源自托管。
 *
 * `@monaco-editor/react` 默认从 cdn.jsdelivr.net 拉 AMD loader,生产 CSP 是
 * `script-src 'self'`,跨域脚本会被拦掉、编辑器永远停在 "Loading..."(见 src/monaco.ts)。
 * 这里把 `monaco-editor/min/vs` 变成同源的 `/vs`:
 *
 *   - dev/preview: 中间件直读 node_modules,不往源码树里拷贝生成物;
 *   - build: 原样拷进 `dist/vs`,由 Caddy 伺服。
 *
 * 用 min/vs(AMD)而不是把 monaco 打进 bundle:保持按需加载,主 bundle 一字节不涨。
 */
function monacoSelfHost() {
  const require = createRequire(import.meta.url);
  const vsDir = resolve(require.resolve("monaco-editor/package.json"), "../min/vs");
  const MIME: Record<string, string> = {
    ".js": "text/javascript; charset=utf-8",
    ".css": "text/css; charset=utf-8",
    ".json": "application/json; charset=utf-8",
    ".ttf": "font/ttf",
    ".map": "application/json; charset=utf-8",
    ".html": "text/html; charset=utf-8",
  };

  return {
    name: "monaco-self-host",
    configureServer(server: any) {
      server.middlewares.use("/vs", (req: any, res: any) => {
        // connect 会把挂载前缀从 req.url 里剥掉,这里拿到的是 /loader.js 这样的相对路径。
        // 一律不 next():放行会落到 SPA fallback,把 index.html 当 JS 喂给 AMD loader。
        const miss = (why: string) => {
          res.statusCode = 404;
          res.setHeader("Content-Type", "text/plain; charset=utf-8");
          res.end(`monaco asset not found: ${why}`);
        };
        const rel = normalize(decodeURIComponent((req.url ?? "/").split("?")[0]));
        if (rel.includes("..")) return miss(rel);
        const file = join(vsDir, rel);
        res.setHeader("Content-Type", MIME[extname(file)] ?? "application/octet-stream");
        createReadStream(file)
          .on("error", () => miss(rel))
          .pipe(res);
      });
    },
    async writeBundle(options: { dir?: string }) {
      await cp(vsDir, resolve(options.dir ?? resolve(__dirname, "dist"), "vs"), {
        recursive: true,
      });
    },
  };
}

/**
 * 本机开发只认一个来源:`http://localhost:<port>`。
 *
 * config 服务的 CORS 白名单(dev.yaml / dev-local.sh)与 Casdoor 的回调地址登记的都是
 * localhost;回调 redirect_uri 又按 window.location.origin 现算(auth/pkce.ts)。
 * 用 127.0.0.1 打开时页面能渲染,但接口被 CORS 拦、登录报 redirect_uri 不匹配。
 * 所以把 127.0.0.1 的请求在 JS 执行前重定向到 localhost,两个地址都能用、来源只有一个。
 *
 * 另一半是「两个回环地址都要连得上」:Vite 只能绑一个 host,默认的 localhost 在 macOS 上
 * 落到 ::1,127.0.0.1 就连接被拒;反过来只绑 127.0.0.1,浏览器解析 localhost 先试 ::1,
 * 不回落的浏览器会直接报「连接失败」。这里在另一个回环地址上开一个 TCP 桥,原样转发到
 * Vite 实际监听的地址(含 HMR 的 WebSocket),不经过任何网卡,不会暴露到局域网。
 */
function canonicalLoopbackOrigin() {
  const redirect = (req: any, res: any, next: () => void) => {
    const host: string = req.headers.host ?? "";
    const match = /^127\.0\.0\.1(:\d+)?$/.exec(host);
    if (!match) return next();
    res.statusCode = 307;
    res.setHeader("Location", `http://localhost${match[1] ?? ""}${req.url ?? "/"}`);
    res.end();
  };

  const bridgeOtherLoopback = (server: any) => {
    const http = server.httpServer;
    if (!http) return; // middlewareMode 下没有自己的 HTTP 服务器
    http.once("listening", () => {
      const addr = http.address();
      if (!addr || typeof addr !== "object") return;
      const other = addr.address === "::1" ? "127.0.0.1" : addr.address === "127.0.0.1" ? "::1" : null;
      if (!other) return; // 绑的是通配地址(--host)或具体网卡,两个回环本来就都通,或者不该管
      const bridge = createNetServer((client) => {
        const upstream = connect(addr.port, addr.address);
        client.pipe(upstream).pipe(client);
        client.on("error", () => upstream.destroy());
        upstream.on("error", () => client.destroy());
      });
      bridge.on("error", (err: NodeJS.ErrnoException) => {
        // 机器没开 IPv6 时 ::1 不可用,属正常,静默跳过
        if (err.code === "EADDRNOTAVAIL") return;
        server.config.logger.warn(`[loopback] 无法在 ${other}:${addr.port} 上桥接(${err.code ?? err.message}),只有 localhost 可用`);
      });
      bridge.listen(addr.port, other);
      http.once("close", () => bridge.close());
    });
  };

  return {
    name: "canonical-loopback-origin",
    configureServer(server: any) {
      bridgeOtherLoopback(server);
      server.middlewares.use(redirect);
    },
    configurePreviewServer(server: any) {
      bridgeOtherLoopback(server);
      server.middlewares.use(redirect);
    },
  };
}

export default defineConfig(({ mode }) => {
  // 判断是否为生产构建（构建命令下 mode 通常为 'production'）
  const isProduction = mode === "production" || process.env.NODE_ENV === "production";

  // 基础测试配置（所有环境共享）
  const baseTestConfig = {
    environment: "jsdom",
    setupFiles: [resolve(__dirname, "./src/test-setup.ts")],
  };

  // 开发环境特有的浏览器测试配置
  const browserTestConfig = {
    browser: {
      enabled: true,
      provider: playwright(), // 使用 Playwright 作为浏览器提供者
      instances: [{ browser: "chromium" }],
      headless: true, // 在 Docker/CI 环境中必须为 true
      ui: false, // 禁用 UI 模式
    },
  };

  // 根据环境合并测试配置
  const testConfig = isProduction
    ? baseTestConfig // 生产环境无需浏览器测试
    : { ...baseTestConfig, ...browserTestConfig };

  return {
    plugins: [
      tanstackRouter({
        target: "react",
        autoCodeSplitting: true,
        routesDirectory: resolve(__dirname, "./src/routes"),
        generatedRouteTree: resolve(__dirname, "./src/routeTree.gen.ts"),
      }),
      monacoSelfHost(),
      canonicalLoopbackOrigin(),
    ],
    test: testConfig,
    server: {
      // host 保持默认(localhost,通常落在 ::1);另一个回环地址由 canonicalLoopbackOrigin 桥接。
      // 不要改成只绑 127.0.0.1:浏览器解析 localhost 先试 ::1,不是每个浏览器都会回落到 IPv4。
      // 也不要用 true/0.0.0.0:那会把开发服务器(含源码)暴露到局域网。
      // 端口被占时自动顺延到下一个可用端口。注意:OAuth 回调 redirect_uri 是按
      // window.location.origin 现算的(auth/pkce.ts),换了端口就要去 Casdoor 应用里
      // 把对应的回调地址也加上,否则登录会被判 redirect_uri 不匹配。
      strictPort: false,
    },
    resolve: {
      alias: {
        "@": resolve(__dirname, "./src"),
      },
    },
  };
});
