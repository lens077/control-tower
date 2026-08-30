/**
 * 应用入口。
 *
 * Load the deploy-time public configuration before importing UI modules.
 */
import { config as zodConfig } from "zod";
import { initI18n } from "@/i18n";
// 必须在任何 Editor 挂载前把 monaco loader 指向同源 /vs,见该模块头部注释
import "@/monaco";
import { loadRuntimeConfig } from "@/runtime-config";

// Zod 4 默认会试 `Function("")` 来探测能不能 JIT 编译校验器。生产 CSP 是
// script-src 'self'(无 unsafe-eval),这次探测必然被拦 —— zod 自己 try/catch 了、
// 功能不受影响,但浏览器仍会在控制台留一条永久的 CSP 违规红字,
// 把真正的错误淹掉(排查时我们就先被它带偏过一次)。显式关掉 JIT 即可消除。
zodConfig({ jitless: true });
import configEn from "./locales/en/config.json";
import configZh from "./locales/zh-CN/config.json";

await loadRuntimeConfig();
await initI18n({
  ns: "config",
  resources: { "zh-CN": configZh, en: configEn },
  titleKey: "app.title",
});
await import("./bootstrap");
