/**
 * 应用入口。
 *
 * Load the deploy-time public configuration before importing UI modules.
 */
import { initI18n } from "@/i18n";
// 必须在任何 Editor 挂载前把 monaco loader 指向同源 /vs,见该模块头部注释
import "@/monaco";
import { loadRuntimeConfig } from "@/runtime-config";
import configEn from "./locales/en/config.json";
import configZh from "./locales/zh-CN/config.json";

await loadRuntimeConfig();
await initI18n({
  ns: "config",
  resources: { "zh-CN": configZh, en: configEn },
  titleKey: "app.title",
});
await import("./bootstrap");
