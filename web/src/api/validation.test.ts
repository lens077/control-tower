import { ConnectError, Code } from "@connectrpc/connect";
import { beforeAll, describe, expect, test } from "vite-plus/test";
import { classifyRule, describeError, describeValidationFailure, isSlug, parseValidationFailure } from "@/api/validation";
import { i18next, initI18n } from "@/i18n";
import configEn from "@/locales/en/config.json";
import configZh from "@/locales/zh-CN/config.json";

const t = (key: string, options?: Record<string, unknown>) => i18next.t(key, options) as string;

beforeAll(async () => {
  await initI18n({ ns: "config", resources: { "zh-CN": configZh, en: configEn } });
  await i18next.changeLanguage("zh-CN");
});

describe("parseValidationFailure", () => {
  test("拆出字段名与细节", () => {
    const failure = parseValidationFailure(
      "[invalid_argument] validation error: service_name: does not match regex pattern `^[a-z][a-z0-9-]*$`",
    );
    expect(failure).toEqual({
      field: "service_name",
      detail: "does not match regex pattern `^[a-z][a-z0-9-]*$`",
    });
  });

  test("非校验错误返回 null", () => {
    expect(parseValidationFailure("permission_denied: 403 Forbidden")).toBe(null);
  });
});

describe("classifyRule", () => {
  test("认识 slug 正则", () => {
    expect(classifyRule("does not match regex pattern `^[a-z][a-z0-9-]*$`")).toEqual({ rule: "slug" });
  });

  test("认识长度与数量上限", () => {
    expect(classifyRule("value length must be at most 64 characters")).toEqual({ rule: "maxLength", limit: 64 });
    expect(classifyRule("value length must be at least 1 characters")).toEqual({ rule: "minLength", limit: 1 });
    expect(classifyRule("value must contain at most 16 items")).toEqual({ rule: "maxItems", limit: 16 });
  });

  test("不认识的规则不硬编一句", () => {
    expect(classifyRule("value must be a valid email")).toBe(null);
  });
});

describe("describeValidationFailure", () => {
  const raw = "[invalid_argument] validation error: service_name: does not match regex pattern `^[a-z][a-z0-9-]*$`";

  test("中文：说清楚该怎么写", () => {
    const message = describeValidationFailure(raw, t);
    expect(message).toContain("服务名");
    expect(message).toContain("小写字母");
    expect(message).toContain("observability-admin");
    expect(message).not.toContain("regex");
  });

  test("英文同样可读", async () => {
    await i18next.changeLanguage("en");
    const message = describeValidationFailure(raw, t);
    expect(message).toContain("Service name");
    expect(message).toContain("lowercase letters");
    await i18next.changeLanguage("zh-CN");
  });

  test("未知字段与未知规则退回原文，不丢信息", () => {
    const message = describeValidationFailure("validation error: weird_field: value must be a valid email", t);
    expect(message).toContain("weird_field");
    expect(message).toContain("value must be a valid email");
  });
});

describe("describeError", () => {
  test("校验错误翻成人话", () => {
    const error = new ConnectError(
      "validation error: service_name: does not match regex pattern `^[a-z][a-z0-9-]*$`",
      Code.InvalidArgument,
    );
    expect(describeError(error, t)).toContain("服务名");
  });

  test("非校验错误保持原文", () => {
    const error = new ConnectError("machine token scope does not cover this namespace/environment", Code.PermissionDenied);
    expect(describeError(error, t)).toContain("machine token scope");
  });
});

describe("isSlug", () => {
  test("与 proto 的 pattern 一致", () => {
    expect(isSlug("observability-admin")).toBe(true);
    expect(isSlug("obs1")).toBe(true);
    expect(isSlug("observability管理员")).toBe(false);
    expect(isSlug("Observability")).toBe(false);
    expect(isSlug("1observability")).toBe(false);
    expect(isSlug("obs_admin")).toBe(false);
    expect(isSlug("")).toBe(false);
  });
});
