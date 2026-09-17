import { formatDate, i18next } from "@/i18n";

/** proto 的 google.protobuf.Timestamp 在生成代码里的形状 */
export type Timestamp = { seconds: bigint; nanos: number };

export function toDate(ts?: Timestamp): Date | null {
  if (!ts) return null;
  return new Date(Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1e6));
}

/** 绝对时间,跟随当前语言;缺省返回「—」。 */
export function fmtAbsolute(ts?: Timestamp, empty = "—"): string {
  const d = toDate(ts);
  return d ? formatDate(d) : empty;
}

/**
 * 「3 分钟前」。列表里扫一眼就知道新旧,精确时间放 tooltip。
 *
 * 是模块级函数,拿不到组件里的 t —— 走 i18next.t 在调用时解析。
 * 调用点在 render 里,切语言时组件会重渲染,文案跟着变。
 */
export function fmtRelative(ts?: Timestamp, empty = "—"): string {
  const d = toDate(ts);
  if (!d) return empty;
  const sec = Math.round((Date.now() - d.getTime()) / 1000);
  if (sec < 60) return i18next.t("config:time.justNow");
  const min = Math.round(sec / 60);
  if (min < 60) return i18next.t("config:time.minutes", { value: min });
  const hour = Math.round(min / 60);
  if (hour < 24) return i18next.t("config:time.hours", { value: hour });
  const day = Math.round(hour / 24);
  if (day < 30) return i18next.t("config:time.days", { value: day });
  return formatDate(d);
}
