/**
 * 设计令牌:云白工作台。
 *
 * 原则:正文与面板全部无彩色(云白 / 雾 / 板岩),颜色只住在边缘 ——
 * 环境用 2px 色带,选中 / 焦点用紫罗兰,错误用玫瑰,成功用薄荷。
 * 顶栏下沿的一条虹彩发丝线是整个界面唯一的「装饰」,其余颜色都承载状态。
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

/** 墨:四级无彩色文字。全部在白底上 ≥ 4.5:1。 */
export const ink = {
  strong: "#1F2630",
  body: "#3A434F",
  muted: "#5A6470",
  faint: "#6E7886",
} as const;

/** 地:云白与雾。 */
export const ground = {
  cloud: "#FFFFFF",
  mist: "#F6F7F9",
  mistDeep: "#EEF0F4",
  line: "#E4E7EC",
  lineStrong: "#D3D8E0",
} as const;

/** 色带:只作 1–2px 边缘与浅底,不作文字。 */
export const band = {
  mint: "#8FDCC4",
  sky: "#9CC6F0",
  amber: "#F3C98B",
  rose: "#F4A9C1",
  violet: "#A79BF2",
  slate: "#C3C9D2",
} as const;

/** 状态色:可作文字的深版本。 */
export const state = {
  active: "#6B5BD6",
  activeSoft: "#EFEDFC",
  success: "#1F8A6A",
  successSoft: "#E6F7F1",
  warning: "#9A6A0C",
  warningSoft: "#FBF3E3",
  danger: "#C7384F",
  dangerSoft: "#FCEBEF",
  info: "#2F6FB5",
  infoSoft: "#EAF2FC",
} as const;

/** 顶栏下沿 / 焦点态 / 主按钮边缘的虹彩线:薄荷 → 紫罗兰 → 玫瑰,用色带原色,在白底上要看得见。 */
export const sheen = `linear-gradient(90deg, ${band.mint} 0%, ${band.violet} 50%, ${band.rose} 100%)`;

/** 云纸颗粒:雾面板与弹层纸面的细微纹理(public/grain.svg,程序化生成)。 */
export const grain = {
  backgroundImage: 'url("/grain.svg")',
  backgroundSize: "160px 160px",
} as const;

/** 一条 2px 的虹彩边:贴在元素底沿。 */
export const sheenEdge = (height = 2) =>
  ({
    backgroundImage: sheen,
    backgroundRepeat: "no-repeat",
    backgroundSize: `100% ${height}px`,
    backgroundPosition: "bottom",
  }) as const;

export const font = {
  sans: [
    "'Source Sans 3 Variable'",
    "-apple-system",
    "BlinkMacSystemFont",
    "'PingFang SC'",
    "'Hiragino Sans GB'",
    "'Microsoft YaHei'",
    "'Segoe UI'",
    "sans-serif",
  ].join(","),
  mono: [
    "'JetBrains Mono Variable'",
    "ui-monospace",
    "SFMono-Regular",
    "Menlo",
    "Consolas",
    "monospace",
  ].join(","),
} as const;

/** 环境 → 色带与文字色。未知环境退到板岩。 */
export interface EnvTone {
  band: string;
  text: string;
  soft: string;
}

const ENV_TONES: Record<string, EnvTone> = {
  dev: { band: band.mint, text: state.success, soft: state.successSoft },
  uat: { band: band.sky, text: state.info, soft: state.infoSoft },
  pre: { band: band.amber, text: state.warning, soft: state.warningSoft },
  prod: { band: band.rose, text: state.danger, soft: state.dangerSoft },
};

export function envTone(env: string): EnvTone {
  return ENV_TONES[env] ?? { band: band.slate, text: ink.muted, soft: ground.mistDeep };
}

/** 发丝边框。 */
export const hairline = `1px solid ${ground.line}`;

/** 全屏覆盖层:铺满视口盖住其它一切。
 * 用 CSS 覆盖而不是浏览器原生 Fullscreen API —— 后者要处理 fullscreenchange、
 * 权限失败回退,而且会连地址栏一起隐藏,对「专心看一份配置」来说反而碍事。 */
export const fullscreenOverlay = {
  position: "fixed",
  inset: 0,
  zIndex: (theme: { zIndex: { modal: number } }) => theme.zIndex.modal + 1,
  m: 0,
  borderRadius: 0,
  maxWidth: "none",
} as const;
