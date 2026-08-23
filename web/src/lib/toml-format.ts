/**
 * TOML 轻量格式化。
 *
 * 为什么不用 `parse` -> `stringify`:那条路会把整份文档过一遍对象模型,
 * 注释、空行、键的书写顺序全部丢失 —— 对配置中心来说注释往往比值还重要。
 * 所以这里只在「行」这一层做规范化,不碰任何值:
 *   - 顶层的 `key = value` 去掉多余缩进,等号两侧各留一个空格
 *   - 表头 `[a.b]` / `[[a.b]]` 顶格,并保证它(及其上方的注释块)前面有且只有一个空行
 *   - 去行尾空白、折叠连续空行、去首尾空行、结尾补一个换行
 * 多行字符串(""" / ''')和跨行的数组/内联表内部**原样保留**,不做任何改动。
 *
 * 代价是不会重排键顺序、不会折叠数组 —— 这正是保留注释所必须付出的。
 */

interface ScanState {
  /** 当前是否处于多行字符串内部,值为其结束定界符 */
  multiline: '"""' | "'''" | null;
  /** 未闭合的 [ / { 层数,>0 表示跨行的数组或内联表 */
  depth: number;
}

/**
 * 扫描一行,推进 state,并返回该行顶层 `=` 的下标(没有则为 -1)。
 * 之所以要手写扫描而不是用正则:`=`、`#`、`[` 都可能出现在字符串字面量里。
 */
function scanLine(line: string, state: ScanState): number {
  let i = 0;
  let eqIndex = -1;
  const topLevelAtStart = state.depth === 0 && state.multiline === null;

  // 上一行留下的多行字符串:先找结束定界符
  if (state.multiline) {
    const close = line.indexOf(state.multiline);
    if (close === -1) return -1;
    i = close + 3;
    state.multiline = null;
  }

  while (i < line.length) {
    const c = line[i];

    if (c === "#") break; // 注释直到行尾

    if (line.startsWith('"""', i) || line.startsWith("'''", i)) {
      const delim = line.slice(i, i + 3) as '"""' | "'''";
      const close = line.indexOf(delim, i + 3);
      if (close === -1) {
        state.multiline = delim;
        break;
      }
      i = close + 3;
      continue;
    }

    if (c === '"') {
      i++;
      while (i < line.length) {
        if (line[i] === "\\") i += 2;
        else if (line[i] === '"') {
          i++;
          break;
        } else i++;
      }
      continue;
    }

    if (c === "'") {
      // 字面量字符串没有转义
      i++;
      while (i < line.length && line[i] !== "'") i++;
      i++;
      continue;
    }

    if (c === "[" || c === "{") {
      state.depth++;
      i++;
      continue;
    }

    if (c === "]" || c === "}") {
      state.depth--;
      i++;
      continue;
    }

    if (c === "=" && eqIndex === -1 && topLevelAtStart && state.depth === 0) {
      eqIndex = i;
    }
    i++;
  }

  return eqIndex;
}

/** 表头前插一个空行;若表头上方紧贴着注释块,空行要插在注释块之前 */
function insertBlankBeforeHeader(out: string[]): void {
  let j = out.length;
  while (j > 0 && out[j - 1].trimStart().startsWith("#")) j--;
  if (j > 0 && out[j - 1] !== "") out.splice(j, 0, "");
}

export function formatToml(src: string): string {
  const lines = src.split(/\r\n|\n|\r/);
  const state: ScanState = { multiline: null, depth: 0 };
  const out: string[] = [];

  for (const raw of lines) {
    const insideMultiline = state.multiline !== null;
    const insideBrackets = state.depth > 0;
    const eqIndex = scanLine(raw, state);

    // 多行字符串内部一个字符都不能动
    if (insideMultiline) {
      out.push(raw);
      continue;
    }
    // 跨行数组/内联表内部只去行尾空白,缩进保留用户写法
    if (insideBrackets) {
      out.push(raw.trimEnd());
      continue;
    }

    const trimmed = raw.trim();

    if (trimmed === "") {
      if (out.length > 0 && out[out.length - 1] !== "") out.push("");
      continue;
    }

    // 顶层以 [ 开头的只可能是表头(值必须写成 `key = [...]`)
    if (trimmed.startsWith("[")) {
      if (out.length > 0) insertBlankBeforeHeader(out);
      out.push(trimmed);
      continue;
    }

    if (trimmed.startsWith("#")) {
      out.push(trimmed);
      continue;
    }

    if (eqIndex !== -1) {
      const key = raw.slice(0, eqIndex).trim();
      const rest = raw.slice(eqIndex + 1);
      // 这行开了个多行字符串(`key = """abc   `)时行尾空白属于字符串内容,不能剪
      const value = state.multiline ? rest.replace(/^\s+/, "") : rest.trim();
      out.push(`${key} = ${value}`);
      continue;
    }

    out.push(trimmed);
  }

  while (out.length > 0 && out[out.length - 1] === "") out.pop();
  return out.length === 0 ? "" : out.join("\n") + "\n";
}
