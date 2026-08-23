/**
 * 玻璃态(glassmorphism)设计令牌与 sx 片段。
 * 原则:半透明 + 背景模糊 + 发丝边框 + 柔和阴影 + 大圆角。
 *
 * 注意:MUI 的 sx 会把 `p/m/gap` 等间距数字按 8px 系数换算,
 * 因此这里的像素间距一律用字符串(sp),避免被 ×8。
 */

export const sp = {
  0: "0px",
  1: "4px",
  2: "8px",
  3: "12px",
  4: "16px",
  5: "20px",
  6: "24px",
  8: "32px",
  10: "40px",
  12: "48px",
  16: "64px",
} as const;

// 页面渐变背景(供玻璃面板浮于其上)
export const appBackground =
  "radial-gradient(1200px 600px at 10% 0%, #dbeafe 0%, transparent 55%)," +
  "radial-gradient(1000px 600px at 90% 10%, #fae8ff 0%, transparent 50%)," +
  "linear-gradient(160deg, #f5f7fb 0%, #eef1f7 100%)";

// 通用玻璃面板样式
export const glassPanel = {
  background: "rgba(255, 255, 255, 0.55)",
  backdropFilter: "blur(20px) saturate(180%)",
  WebkitBackdropFilter: "blur(20px) saturate(180%)",
  border: "1px solid rgba(255, 255, 255, 0.35)",
  boxShadow: "0 8px 32px rgba(31, 38, 135, 0.12)",
  borderRadius: "16px",
} as const;

// 全屏覆盖层:铺满视口盖住其它一切。
// 用 CSS 覆盖而不是浏览器原生 Fullscreen API —— 后者要处理 fullscreenchange、
// 权限失败回退,而且会连地址栏一起隐藏,对「专心看一份配置」来说反而碍事。
export const fullscreenOverlay = {
  position: "fixed",
  inset: 0,
  zIndex: (theme: { zIndex: { modal: number } }) => theme.zIndex.modal + 1,
  m: 0,
  borderRadius: 0,
  maxWidth: "none",
} as const;

// 更强的玻璃(用于弹层/卡片悬浮)
export const glassStrong = {
  background: "rgba(255, 255, 255, 0.72)",
  backdropFilter: "blur(28px) saturate(200%)",
  WebkitBackdropFilter: "blur(28px) saturate(200%)",
  border: "1px solid rgba(255, 255, 255, 0.5)",
  boxShadow: "0 12px 40px rgba(31, 38, 135, 0.18)",
  borderRadius: "20px",
} as const;
