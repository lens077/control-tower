import { ConnectError } from "@connectrpc/connect";

// 服务端 protovalidate 的报错是英文机器文案，例如：
//
//   validation error: service_name: does not match regex pattern `^[a-z][a-z0-9-]*$`
//
// 直接甩给用户既看不懂也不告诉他该怎么改。这里把它拆成「字段 + 规则」，
// 交给 i18n 渲染成中英文两种人话；拆不出来的原样透传，绝不吞掉原始信息。

/** 后端字段名（snake_case）与失败细节。 */
export interface ValidationFailure {
  field: string;
  detail: string;
}

const VALIDATION_MESSAGE = /validation error:\s*([A-Za-z0-9_.[\]]+):\s*(.+)$/;

/** 从 Connect 错误文案里解析出字段级校验失败；不是校验错误时返回 null。 */
export function parseValidationFailure(message: string): ValidationFailure | null {
  const matched = VALIDATION_MESSAGE.exec(message.trim());
  if (!matched) return null;
  return { field: matched[1], detail: matched[2].trim() };
}

/** 规则标识；与 locales 里 errors.validation.rules.* 一一对应。 */
export type ValidationRule = "slug" | "minLength" | "maxLength" | "maxItems" | "required";

export interface DescribedRule {
  rule: ValidationRule;
  /** 规则参数，例如长度上限；用于 i18n 插值。 */
  limit?: number;
}

const SLUG_PATTERN = "^[a-z][a-z0-9-]*$";

/**
 * classifyRule 把 protovalidate 的细节文案归类到有限几种规则。
 * 只认我们自己 proto 里真正用到的那几条；新增约束时在这里补一条，
 * 顺带在 locales 里补文案，未知规则会退回到原始英文而不是编一句。
 */
export function classifyRule(detail: string): DescribedRule | null {
  if (detail.includes("does not match regex pattern") && detail.includes(SLUG_PATTERN)) {
    return { rule: "slug" };
  }
  const minLength = /value length must be at least (\d+)/.exec(detail);
  if (minLength) return { rule: "minLength", limit: Number(minLength[1]) };
  const maxLength = /value length must be at most (\d+)/.exec(detail);
  if (maxLength) return { rule: "maxLength", limit: Number(maxLength[1]) };
  const maxItems = /value must contain at most (\d+) item/.exec(detail);
  if (maxItems) return { rule: "maxItems", limit: Number(maxItems[1]) };
  if (detail.includes("value is required")) return { rule: "required" };
  return null;
}

/** 本地表单校验用的 slug 规则，和 proto 的 pattern 保持一致。 */
export const slugPattern = new RegExp(SLUG_PATTERN);

/**
 * isSlug 判断一段文本是否满足 `^[a-z][a-z0-9-]*$`。
 * 表单在提交前用它拦一次，省掉一个来回，也避免用户对着英文正则猜。
 */
export function isSlug(value: string): boolean {
  return slugPattern.test(value);
}

type Translate = (key: string, options?: Record<string, unknown>) => string;

/**
 * describeValidationFailure 把一条服务端校验错误渲染成当前语言的句子。
 * 字段名或规则任一不认识时，退回到带原文的兜底文案——宁可显得笨，
 * 也不能把后端真正说的话弄丢。
 */
export function describeValidationFailure(message: string, t: Translate): string | null {
  const failure = parseValidationFailure(message);
  if (!failure) return null;

  const fieldKey = `errors.validation.fields.${failure.field}`;
  const fieldLabel = t(fieldKey);
  const field = fieldLabel === fieldKey ? failure.field : fieldLabel;

  const classified = classifyRule(failure.detail);
  if (!classified) {
    return t("errors.validation.fallback", { field, detail: failure.detail });
  }
  return t(`errors.validation.rules.${classified.rule}`, { field, limit: classified.limit });
}

/**
 * describeError 是 UI 展示任意 RPC 错误的统一入口：校验错误翻成人话，
 * 其余（权限、网络、服务端故障）保持原始文案——那些消息本来就是给人看的，
 * 硬翻只会丢信息。
 */
export function describeError(error: unknown, t: Translate): string {
  const message = ConnectError.from(error).message || t("errors.requestFailed");
  return describeValidationFailure(message, t) ?? message;
}
