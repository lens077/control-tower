import { describe, expect, it } from "vite-plus/test";
import { lineDelta } from "./linediff";

describe("lineDelta", () => {
  it("完全相同 -> 零增零删", () => {
    expect(lineDelta("a\nb\nc\n", "a\nb\nc\n")).toEqual({ added: 0, removed: 0 });
  });

  it("改一行 -> 一增一删", () => {
    const before = "server:\n  addr: 30006\n  timeout: 5s\n";
    const after = "server:\n  addr: 39999\n  timeout: 5s\n";
    expect(lineDelta(before, after)).toEqual({ added: 1, removed: 1 });
  });

  it("纯新增", () => {
    expect(lineDelta("a\nb\n", "a\nb\nc\nd\n")).toEqual({ added: 2, removed: 0 });
  });

  it("纯删除", () => {
    expect(lineDelta("a\nb\nc\n", "a\n")).toEqual({ added: 0, removed: 2 });
  });

  it("空 -> 有内容:整份算新增", () => {
    expect(lineDelta("", "a\nb\n")).toEqual({ added: 2, removed: 0 });
  });

  // 末尾换行只是行终止符,不该被当成多出来的一行空行,
  // 否则「保存时顺手补了个换行」会被报成 +1 行。
  it("末尾换行不算一行", () => {
    expect(lineDelta("a\nb", "a\nb\n")).toEqual({ added: 0, removed: 0 });
  });

  // 中间插入不该被算成「后面所有行都变了」—— 这正是逐行比对做不到、必须求 LCS 的地方
  it("中间插入只算新增的那一行", () => {
    expect(lineDelta("a\nb\nc\n", "a\nx\nb\nc\n")).toEqual({ added: 1, removed: 0 });
  });

  it("整份重写", () => {
    expect(lineDelta("a\nb\nc\n", "x\ny\n")).toEqual({ added: 2, removed: 3 });
  });

  // 上千行的整份替换会撞上 MAX_CELLS 的兜底分支:结果退化成「整段替换」,
  // 但必须仍然返回、且是有限时间内返回
  it("超大输入走兜底分支且不卡住", () => {
    const a = Array.from({ length: 800 }, (_, i) => `old-${i}`).join("\n");
    const b = Array.from({ length: 800 }, (_, i) => `new-${i}`).join("\n");
    expect(lineDelta(a, b)).toEqual({ added: 800, removed: 800 });
  });
});
