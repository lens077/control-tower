/**
 * 配置内容的「格式校验 + 格式化」统一入口。
 *
 * 三种格式的解析器报错形状各不相同(YAML 给 linePos、TOML 给 line/column、
 * JSON 只给一句英文里夹着的 position),这里把它们统一成 FormatIssue,
 * 好让 edit.tsx 直接喂给 Monaco 的 setModelMarkers,不用关心是哪种格式。
 */

import { parseDocument } from "yaml";
import { parse as parseToml, TomlError } from "smol-toml";
import { type ParseError, parse as parseJsonc } from "jsonc-parser";
import { i18next } from "@/i18n";
import { ConfigFormat } from "@/gen/api";
import { formatToml } from "@/lib/toml-format";

/** 一条格式错误。行列均为 1-based,与 Monaco marker 的约定一致。 */
export interface FormatIssue {
  line: number;
  column: number;
  endLine: number;
  endColumn: number;
  message: string;
}

export interface ValidateResult {
  ok: boolean;
  issues: FormatIssue[];
}

export const VALID: ValidateResult = { ok: true, issues: [] };

/** 字符偏移 -> 1-based 行列。JSON 和兜底路径共用。 */
export function offsetToPos(text: string, offset: number): { line: number; column: number } {
  const clamped = Math.max(0, Math.min(offset, text.length));
  const before = text.slice(0, clamped);
  const lines = before.split(/\r\n|\n|\r/);
  return { line: lines.length, column: lines[lines.length - 1].length + 1 };
}

function issueAt(line: number, column: number, message: string, span = 1): FormatIssue {
  return { line, column, endLine: line, endColumn: column + span, message };
}

/** 空白内容一律放行:新建 key 时编辑器是空的,不该一上来就报错、把保存按钮锁死。 */
function isBlank(text: string): boolean {
  return text.trim() === "";
}

// ---------------------------------------------------------------- JSON

/**
 * 为什么不用 `JSON.parse` 的报错定位:V8 只在部分错误上给 position,
 * 例如 Node 24 下 `{"a":}` 只会说 `Unexpected token '}', "{"a":}" is not valid JSON`
 * —— 一个位置都没有,行号只能瞎猜。jsonc-parser(Monaco 自己也在用)给的是
 * 精确的 offset + length,所以校验走它,严格模式关掉注释和尾逗号。
 */
// jsonc-parser 的错误码 -> 文案 key。key 显式列出而不是拼 `validate.json.${code}`,
// 否则提取工具扫不到,将来漏翻也检测不出来。
const JSON_ERROR_KEYS = {
  1: "config:validate.json.1",
  2: "config:validate.json.2",
  3: "config:validate.json.3",
  4: "config:validate.json.4",
  5: "config:validate.json.5",
  6: "config:validate.json.6",
  7: "config:validate.json.7",
  8: "config:validate.json.8",
  9: "config:validate.json.9",
  10: "config:validate.json.10",
  11: "config:validate.json.11",
  12: "config:validate.json.12",
  13: "config:validate.json.13",
  14: "config:validate.json.14",
  15: "config:validate.json.15",
  16: "config:validate.json.16",
} as const;

/** 文案在调用时才解析 —— 这是模块级的表,存翻译好的字符串会被语言切换甩在后面。 */
function jsonErrorMessage(code: number): string {
  const key = JSON_ERROR_KEYS[code as keyof typeof JSON_ERROR_KEYS];
  return key ? i18next.t(key) : i18next.t("config:validate.json.unknown", { code });
}

function jsonIssues(text: string): FormatIssue[] {
  const errors: ParseError[] = [];
  parseJsonc(text, errors, { allowTrailingComma: false, disallowComments: true });
  return errors.map((e) => {
    const { line, column } = offsetToPos(text, e.offset);
    return issueAt(line, column, jsonErrorMessage(e.error), Math.max(1, e.length));
  });
}

// ---------------------------------------------------------------- YAML

/**
 * prettyErrors(v2 默认开启)让 linePos 可用,但代价是 message 后面会拖一截
 * ` at line 3, column 1:\n\n<代码预览>` —— 位置我们已经单独拿到了,这截去掉。
 */
function cleanYamlMessage(message: string): string {
  return message.replace(/ at line \d+, column \d+:[\s\S]*$/, "").trim() || message;
}

function yamlIssues(text: string): FormatIssue[] {
  const doc = parseDocument(text);
  return doc.errors.map((e) => {
    const message = cleanYamlMessage(e.message);
    const start = e.linePos?.[0];
    const end = e.linePos?.[1];
    if (!start) {
      const p = offsetToPos(text, e.pos?.[0] ?? 0);
      return issueAt(p.line, p.column, message);
    }
    return {
      line: start.line,
      column: start.col,
      endLine: end?.line ?? start.line,
      endColumn: end?.col ?? start.col + 1,
      message,
    };
  });
}

// ---------------------------------------------------------------- TOML

function tomlIssues(text: string): FormatIssue[] {
  try {
    parseToml(text);
    return [];
  } catch (e) {
    if (e instanceof TomlError) {
      // TomlError.message 里带了一整块 codeblock 预览,行列我们已经单独有了
      const message = e.message.split("\n")[0].replace(/^Invalid TOML document:\s*/, "");
      return [issueAt(e.line, e.column, message)];
    }
    return [issueAt(1, 1, e instanceof Error ? e.message : String(e))];
  }
}

// ---------------------------------------------------------------- 对外

/** 按所选格式校验内容。注意是「下拉里选的格式」,与 key 名/后缀无关。 */
export function validateContent(text: string, format: ConfigFormat): ValidateResult {
  if (isBlank(text)) return VALID;

  let issues: FormatIssue[] = [];
  switch (format) {
    case ConfigFormat.JSON:
      issues = jsonIssues(text);
      break;
    case ConfigFormat.YAML:
      issues = yamlIssues(text);
      break;
    case ConfigFormat.TOML:
      issues = tomlIssues(text);
      break;
    default: // PLAINTEXT / UNSPECIFIED:没有可校验的语法
      break;
  }
  return { ok: issues.length === 0, issues };
}

/** PLAINTEXT 无从格式化,其余三种都支持。 */
export function canFormat(format: ConfigFormat): boolean {
  return (
    format === ConfigFormat.JSON || format === ConfigFormat.YAML || format === ConfigFormat.TOML
  );
}

/**
 * 按所选格式重排内容。解析不过时抛出 FormatError(带行列),
 * 调用方可以直接把它标到编辑器上。
 */
export class FormatError extends Error {
  constructor(
    message: string,
    readonly issues: FormatIssue[],
  ) {
    super(message);
    this.name = "FormatError";
  }
}

export function formatContent(text: string, format: ConfigFormat): string {
  if (isBlank(text)) return text;

  const check = validateContent(text, format);
  if (!check.ok) {
    const first = check.issues[0];
    throw new FormatError(
      i18next.t("config:validate.position", {
        line: first.line,
        column: first.column,
        message: first.message,
      }),
      check.issues,
    );
  }

  switch (format) {
    case ConfigFormat.JSON:
      return JSON.stringify(JSON.parse(text), null, 2) + "\n";
    case ConfigFormat.YAML:
      // 走 Document 而不是 stringify(parse(x)):注释和锚点才留得住
      return parseDocument(text).toString({ indent: 2, lineWidth: 0 });
    case ConfigFormat.TOML:
      return formatToml(text);
    default:
      return text;
  }
}
