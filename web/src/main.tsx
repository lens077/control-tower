/**
 * 应用入口。
 *
 * Load the deploy-time public configuration before importing UI modules.
 */
import { initI18n } from "@/i18n";
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
