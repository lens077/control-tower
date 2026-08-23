import { describe, expect, test } from "vite-plus/test";
import { initI18n } from "@/i18n";
import { ConfigFormat } from "@/gen/api";
import configEn from "@/locales/en/config.json";
import configZh from "@/locales/zh-CN/config.json";
import {
  canFormat,
  FormatError,
  formatContent,
  offsetToPos,
  validateContent,
} from "@/lib/validate";
import { formatToml } from "@/lib/toml-format";

// 校验消息走 i18n,不装资源的话 t() 只会把 key 原样吐回来。
// 这里钉死 zh-CN:断言的是中文文案,不该受跑测环境的 navigator.language 影响。
await initI18n({ ns: "config", resources: { "zh-CN": configZh, en: configEn }, locale: "zh-CN" });

describe("offsetToPos", () => {
  test("首字符是 1:1", () => {
    expect(offsetToPos("abc", 0)).toEqual({ line: 1, column: 1 });
  });

  test("换行后行号递增、列重置", () => {
    expect(offsetToPos("ab\ncd", 3)).toEqual({ line: 2, column: 1 });
    expect(offsetToPos("ab\ncd", 5)).toEqual({ line: 2, column: 3 });
  });

  test("CRLF 与 CR 都算一次换行", () => {
    expect(offsetToPos("ab\r\ncd", 4)).toEqual({ line: 2, column: 1 });
    expect(offsetToPos("ab\rcd", 3)).toEqual({ line: 2, column: 1 });
  });

  test("越界偏移被夹到两端而不是抛错", () => {
    expect(offsetToPos("ab", -5)).toEqual({ line: 1, column: 1 });
    expect(offsetToPos("ab", 999)).toEqual({ line: 1, column: 3 });
  });
});

describe("validateContent", () => {
  test("空白内容一律放行(新建 key 时不该锁死保存)", () => {
    for (const f of [
      ConfigFormat.JSON,
      ConfigFormat.YAML,
      ConfigFormat.TOML,
      ConfigFormat.PLAINTEXT,
    ]) {
      expect(validateContent("   \n\n ", f).ok).toBe(true);
    }
  });

  test("PLAINTEXT 恒通过", () => {
    expect(validateContent("{ 这既不是 JSON 也不是 YAML", ConfigFormat.PLAINTEXT).ok).toBe(true);
  });

  test("坏 JSON 报出所在行(V8 报错常常不带 position,所以走 jsonc-parser)", () => {
    const r = validateContent('{\n  "a": 1,\n  "b":,\n}', ConfigFormat.JSON);
    expect(r.ok).toBe(false);
    expect(r.issues[0].line).toBe(3);
    expect(r.issues[0].message).toBe("此处应为一个值");
  });

  test("JSON 不接受注释和尾逗号", () => {
    expect(validateContent('{\n  // 注释\n  "a": 1\n}', ConfigFormat.JSON).ok).toBe(false);
    expect(validateContent('{\n  "a": 1,\n}', ConfigFormat.JSON).ok).toBe(false);
  });

  test("坏 YAML 报出所在行,且消息里不夹代码预览", () => {
    const r = validateContent("a: 1\nb: 2\n  c: 3\n", ConfigFormat.YAML);
    expect(r.ok).toBe(false);
    expect(r.issues[0].line).toBe(2);
    // yaml 的 prettyErrors 会往 message 尾巴上贴 " at line X, column Y:\n\n<预览>"
    expect(r.issues[0].message).not.toContain("\n");
    expect(r.issues[0].message).not.toMatch(/at line \d+, column/);
  });

  test("坏 TOML 报出所在行,且消息里不夹 codeblock 预览", () => {
    const r = validateContent('a = 1\nb = \nc = "x"\n', ConfigFormat.TOML);
    expect(r.ok).toBe(false);
    expect(r.issues[0].line).toBe(2);
    expect(r.issues[0].message).not.toContain("\n");
    expect(r.issues[0].message).not.toMatch(/^Invalid TOML document/);
  });

  test("校验跟的是所选格式,不是内容长相", () => {
    const yaml = "a: 1\nb: 2\n";
    expect(validateContent(yaml, ConfigFormat.YAML).ok).toBe(true);
    expect(validateContent(yaml, ConfigFormat.JSON).ok).toBe(false);
  });

  test("合法内容三种格式都通过", () => {
    expect(validateContent('{"a":1}', ConfigFormat.JSON).ok).toBe(true);
    expect(validateContent("a: 1\nb:\n  - x\n", ConfigFormat.YAML).ok).toBe(true);
    expect(validateContent('a = 1\n[t]\nb = "x"\n', ConfigFormat.TOML).ok).toBe(true);
  });
});

describe("formatContent", () => {
  test("YAML 格式化后注释还在", () => {
    const src = "# 顶部说明\na:   1\nb:\n    - x   # 行尾注释\n";
    const out = formatContent(src, ConfigFormat.YAML);
    expect(out).toContain("# 顶部说明");
    expect(out).toContain("# 行尾注释");
    expect(validateContent(out, ConfigFormat.YAML).ok).toBe(true);
  });

  test("JSON 格式化成两空格缩进", () => {
    expect(formatContent('{"a":1,"b":[2,3]}', ConfigFormat.JSON)).toBe(
      '{\n  "a": 1,\n  "b": [\n    2,\n    3\n  ]\n}\n',
    );
  });

  test("PLAINTEXT 原样返回且不可格式化", () => {
    expect(canFormat(ConfigFormat.PLAINTEXT)).toBe(false);
    expect(formatContent("随便写点什么", ConfigFormat.PLAINTEXT)).toBe("随便写点什么");
  });

  test("解析不过时抛 FormatError,带上行列", () => {
    expect(() => formatContent("{ nope", ConfigFormat.JSON)).toThrow(FormatError);
    try {
      formatContent("{ nope", ConfigFormat.JSON);
    } catch (e) {
      expect(e).toBeInstanceOf(FormatError);
      expect((e as FormatError).issues.length).toBeGreaterThan(0);
      expect((e as FormatError).message).toMatch(/^第 \d+ 行 第 \d+ 列/);
    }
  });

  test("空白内容格式化是 no-op", () => {
    expect(formatContent("  \n", ConfigFormat.YAML)).toBe("  \n");
  });
});

describe("formatToml", () => {
  test("规范化等号两侧空格与缩进,注释保留", () => {
    const src = "# 头部注释\n   a=1\nb    =   2   \n";
    expect(formatToml(src)).toBe("# 头部注释\na = 1\nb = 2\n");
  });

  test("表头顶格,且其上方注释块之前补一个空行", () => {
    const src = 'a = 1\n# 关于 t 的说明\n  [t]\nb = "x"\n';
    expect(formatToml(src)).toBe('a = 1\n\n# 关于 t 的说明\n[t]\nb = "x"\n');
  });

  test("已有的空行不会被叠加成两个", () => {
    expect(formatToml("a = 1\n\n[t]\nb = 2\n")).toBe("a = 1\n\n[t]\nb = 2\n");
  });

  test("连续空行折叠成一个,首尾空行去掉", () => {
    expect(formatToml("\n\na = 1\n\n\n\nb = 2\n\n\n")).toBe("a = 1\n\nb = 2\n");
  });

  test("多行字符串内部原样保留", () => {
    const src = 'a = """\n   缩进和   空格都要留住\n   x=1\n"""\nb = 2\n';
    expect(formatToml(src)).toBe('a = """\n   缩进和   空格都要留住\n   x=1\n"""\nb = 2\n');
  });

  test("跨行数组内部缩进保留", () => {
    const src = "a = [\n    1,\n    2,\n]\nb=3\n";
    expect(formatToml(src)).toBe("a = [\n    1,\n    2,\n]\nb = 3\n");
  });

  test("字符串里的 = # [ 不会被当成语法", () => {
    const src = 'a = "x = 1 # [not-a-table]"\n';
    expect(formatToml(src)).toBe('a = "x = 1 # [not-a-table]"\n');
  });

  test("格式化后仍是合法 TOML", () => {
    const src = '# c\n  a=1\n[[arr]]\nx="1"\n[[arr]]\nx="2"\n';
    const out = formatToml(src);
    expect(validateContent(out, ConfigFormat.TOML).ok).toBe(true);
  });
});
