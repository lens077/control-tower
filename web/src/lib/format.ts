import { i18next } from "@/i18n";
import { ConfigFormat } from "@/gen/api";

// 格式 -> Monaco 语言标识(用于高亮)。
// 说明:Monaco 内置 json/yaml 高亮;toml 无内置语言,退化为 ini(近似高亮)。
export function formatToLanguage(f: ConfigFormat): string {
  switch (f) {
    case ConfigFormat.YAML:
      return "yaml";
    case ConfigFormat.JSON:
      return "json";
    case ConfigFormat.TOML:
      return "ini";
    default:
      return "plaintext";
  }
}

// 前四个是格式名,不翻译;只有兜底那句是文案。取值时才解析,跟着当前语言走。
export function formatLabel(f: ConfigFormat): string {
  switch (f) {
    case ConfigFormat.YAML:
      return "YAML";
    case ConfigFormat.JSON:
      return "JSON";
    case ConfigFormat.TOML:
      return "TOML";
    case ConfigFormat.PLAINTEXT:
      return "PlainText";
    default:
      return i18next.t("config:format.unspecified");
  }
}

// 可选格式(用于下拉),排除 UNSPECIFIED。
export const FORMAT_OPTIONS: ConfigFormat[] = [
  ConfigFormat.YAML,
  ConfigFormat.TOML,
  ConfigFormat.JSON,
  ConfigFormat.PLAINTEXT,
];

// 环境选项
export const ENV_OPTIONS = ["dev", "pre", "prod", "uat"] as const;
