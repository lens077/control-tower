import { describe, expect, it } from "vitest";
import { MetricUnit } from "@/gen/api";
import {
  formatBytes,
  formatDuration,
  formatMetricValue,
  formatMilliseconds,
  usageSeverity,
} from "./metric-format";

describe("formatBytes", () => {
  it("用 1024 进制", () => {
    // 这些数字全部来自操作系统(RSS、cgroup 限额、文件系统),那边一律二进制单位。
    // 用 1000 进制的话页面上的数字会和 kubectl top 对不上。
    expect(formatBytes(1024)).toBe("1.00 KiB");
    expect(formatBytes(1024 * 1024)).toBe("1.00 MiB");
    expect(formatBytes(1536 * 1024 * 1024)).toBe("1.50 GiB");
  });

  it("字节数不带小数,大数值收小数位", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(150 * 1024)).toBe("150 KiB"); // >= 100 不留小数
    expect(formatBytes(15 * 1024)).toBe("15.0 KiB"); // >= 10 留一位
  });

  it("非有限数返回占位而不是 NaN", () => {
    // 后端理论上已经挡掉 NaN,这里是第二道:显示 "NaN B" 比显示 "-" 糟糕得多
    expect(formatBytes(Number.NaN)).toBe("-");
    expect(formatBytes(Number.POSITIVE_INFINITY)).toBe("-");
  });
});

describe("formatMilliseconds", () => {
  it("按量级切换单位", () => {
    expect(formatMilliseconds(0.5)).toBe("500 µs");
    expect(formatMilliseconds(5)).toBe("5.00 ms");
    expect(formatMilliseconds(250)).toBe("250 ms");
    expect(formatMilliseconds(1500)).toBe("1.50 s");
  });

  it("非有限数返回占位", () => {
    expect(formatMilliseconds(Number.NaN)).toBe("-");
  });
});

describe("formatDuration", () => {
  it("只保留两个最大单位", () => {
    // "3d 4h" 比 "3d 4h 12m 7s" 有用 —— 后者的精度在这里没有意义
    expect(formatDuration(3 * 86400 + 4 * 3600 + 12 * 60 + 7)).toBe("3d 4h");
    expect(formatDuration(3600 + 90)).toBe("1h 1m");
    expect(formatDuration(45)).toBe("45s");
  });

  it("零与负数不产生怪字符串", () => {
    expect(formatDuration(0)).toBe("0s");
    expect(formatDuration(-1)).toBe("-");
  });
});

describe("formatMetricValue", () => {
  it("按量纲选格式,而不是按指标名", () => {
    expect(formatMetricValue(63.877, MetricUnit.PERCENT)).toBe("63.9%");
    expect(formatMetricValue(2048, MetricUnit.BYTES)).toBe("2.00 KiB");
    expect(formatMetricValue(2048, MetricUnit.BYTES_PER_SECOND)).toBe("2.00 KiB/s");
    expect(formatMetricValue(12.5, MetricUnit.MILLISECONDS)).toBe("13 ms");
    expect(formatMetricValue(3.5, MetricUnit.REQUESTS_PER_SECOND)).toBe("3.5 req/s");
  });

  it("未知量纲退化成纯数字并去掉尾随零", () => {
    expect(formatMetricValue(12, MetricUnit.COUNT)).toBe("12");
    expect(formatMetricValue(12.3, MetricUnit.COUNT)).toBe("12.3");
    expect(formatMetricValue(12.0, MetricUnit.UNSPECIFIED)).toBe("12");
  });
});

describe("usageSeverity", () => {
  it("阈值 75/90", () => {
    // 这是给人看的仪表盘不是告警规则,早一点变黄比晚一点有用
    expect(usageSeverity(0)).toBe("success");
    expect(usageSeverity(74.9)).toBe("success");
    expect(usageSeverity(75)).toBe("warning");
    expect(usageSeverity(89.9)).toBe("warning");
    expect(usageSeverity(90)).toBe("error");
    expect(usageSeverity(150)).toBe("error");
  });
});
