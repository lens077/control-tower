/**
 * 版本历史列表用的「这一版改了多少行」统计。
 *
 * 只算行数,不产出补丁 —— 补丁交给 Monaco 的 DiffEditor 去渲染,
 * 这里要的是能塞进一行列表项的摘要。
 */

export interface LineDelta {
  added: number;
  removed: number;
}

/**
 * 超过这个格子数就不做 LCS 了。
 * 一份配置几百行,掐掉公共前后缀后通常只剩个位数;
 * 真碰上整份重写的极端情况,宁可给个粗糙但 O(n) 的结果,
 * 也不要在渲染列表时把主线程卡住。
 */
const MAX_CELLS = 250_000;

function splitLines(text: string): string[] {
  if (text === "") return [];
  const lines = text.split("\n");
  // 文本末尾的换行不该算成一行空行
  if (lines[lines.length - 1] === "") lines.pop();
  return lines;
}

/** 统计从 oldText 到 newText 新增/删除了多少行。 */
export function lineDelta(oldText: string, newText: string): LineDelta {
  if (oldText === newText) return { added: 0, removed: 0 };

  const oldLines = splitLines(oldText);
  const newLines = splitLines(newText);

  // 掐掉公共前缀/后缀:配置改动几乎总是局部的,这一步把 LCS 的规模压到很小
  let start = 0;
  while (
    start < oldLines.length &&
    start < newLines.length &&
    oldLines[start] === newLines[start]
  ) {
    start++;
  }
  let endOld = oldLines.length;
  let endNew = newLines.length;
  while (endOld > start && endNew > start && oldLines[endOld - 1] === newLines[endNew - 1]) {
    endOld--;
    endNew--;
  }

  const o = oldLines.slice(start, endOld);
  const n = newLines.slice(start, endNew);

  // 一侧为空 = 纯新增或纯删除;规模过大 = 按整段替换算
  if (o.length === 0 || n.length === 0 || o.length * n.length > MAX_CELLS) {
    return { added: n.length, removed: o.length };
  }

  // 滚动数组求 LCS 长度。两条数组的下标 0 恒为 0(从不写入),不需要每轮清零。
  let prev = new Uint32Array(n.length + 1);
  let cur = new Uint32Array(n.length + 1);
  for (let i = 1; i <= o.length; i++) {
    for (let j = 1; j <= n.length; j++) {
      cur[j] = o[i - 1] === n[j - 1] ? prev[j - 1] + 1 : Math.max(prev[j], cur[j - 1]);
    }
    const swap = prev;
    prev = cur;
    cur = swap;
  }
  const lcs = prev[n.length];

  return { added: n.length - lcs, removed: o.length - lcs };
}
