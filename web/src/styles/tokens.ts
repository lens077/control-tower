/**
 * 设计令牌:云白工作台。
 *
 * 原则:正文与面板全部无彩色(云白 / 雾 / 板岩),颜色只住在边缘 ——
 * 环境用 2px 色带,选中 / 焦点用紫罗兰,错误用玫瑰,成功用薄荷。
 * 顶栏下沿的一条虹彩发丝线是整个界面唯一的「装饰」,其余颜色都承载状态。
 *
 * 白天与黑夜共用同一套令牌名:组件拿到的是 `var(--cc-…)`,
 * 具体色值由 MuiCssBaseline 按当前模式挂到 :root(见 styles/theme.ts)。
 *
 * 注意:MUI 的 sx 会把 `p/m/gap` 等间距数字按 8px 系数换算,
 * 因此这里的像素间距一律用字符串(sp),避免被 ×8。
 */

import type { ColorMode } from "./color-mode";

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

/**
 * 白天与黑夜两套色板。两边的键必须一一对应(tokens.test.ts 守这一条)。
 *
 * - ink:四级无彩色文字,在各自的 ground.cloud 上全部 ≥ 4.5:1;
 * - ground:云白与雾(黑夜里是夜色与雾);
 * - band:色带,只作 1–2px 边缘与浅底,不作文字;
 * - state:状态色,可作文字;
 * - ui:少数控件专用色(主按钮纸面、提示框边框、选区、tooltip 等)。
 *   primaryVeil 是主按钮盖在虹彩上的那层纸:白天半透,透出彩虹;黑夜几乎不透,
 *   否则浅色文字压在饱和的彩虹上对比度不够。
 */
const lightPalette = {
  ink: {
    strong: "#1F2630",
    body: "#3A434F",
    muted: "#5A6470",
    faint: "#6E7886",
  },
  ground: {
    cloud: "#FFFFFF",
    mist: "#F6F7F9",
    mistDeep: "#EEF0F4",
    line: "#E4E7EC",
    lineStrong: "#D3D8E0",
  },
  band: {
    mint: "#8FDCC4",
    sky: "#9CC6F0",
    amber: "#F3C98B",
    rose: "#F4A9C1",
    violet: "#A79BF2",
    slate: "#C3C9D2",
  },
  state: {
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
  },
  ui: {
    lineHover: "#B9C0CA",
    textDisabled: "#A3ABB6",
    onPrimary: "#FFFFFF",
    primaryFill: "#E4DEFF",
    primaryFillHover: "#D6CEFF",
    primaryVeil: "#A79BF266",
    primaryVeilHover: "#A79BF299",
    dangerFillHover: "#F9DCE3",
    dangerBorder: "#F4C4CD",
    warningBorder: "#EBD8AE",
    infoBorder: "#C9DDF3",
    successBorder: "#BDE8D8",
    selection: "#A79BF2",
    tooltip: "#1F2630",
    tooltipText: "#FFFFFF",
  },
};

export type Palette = typeof lightPalette;

const darkPalette: Palette = {
  ink: {
    strong: "#E6E9EE",
    body: "#C9CFD8",
    muted: "#A0A9B5",
    faint: "#8B94A1",
  },
  ground: {
    cloud: "#15181D",
    mist: "#1B1F25",
    mistDeep: "#232830",
    line: "#2B313A",
    lineStrong: "#3A414C",
  },
  band: {
    mint: "#4FB594",
    sky: "#5E97D0",
    amber: "#C9974A",
    rose: "#D46F91",
    violet: "#8577E0",
    slate: "#4A525E",
  },
  state: {
    active: "#A99BFA",
    activeSoft: "#2A2547",
    success: "#52C7A0",
    successSoft: "#16302A",
    warning: "#E3B45A",
    warningSoft: "#33291A",
    danger: "#F2839A",
    dangerSoft: "#3B1D26",
    info: "#7AAEEA",
    infoSoft: "#182A3F",
  },
  ui: {
    lineHover: "#4F5865",
    textDisabled: "#5E6773",
    onPrimary: "#15181D",
    primaryFill: "#2D2750",
    primaryFillHover: "#3A3268",
    primaryVeil: "#2D2750E0",
    primaryVeilHover: "#3A3268D0",
    dangerFillHover: "#4A2331",
    dangerBorder: "#5C2B38",
    warningBorder: "#54431F",
    infoBorder: "#264463",
    successBorder: "#1F4C3E",
    selection: "#4A3F9E",
    tooltip: "#2E343D",
    tooltipText: "#E6E9EE",
  },
};

export const palettes: Record<ColorMode, Palette> = { light: lightPalette, dark: darkPalette };

/** 阴影与遮罩:白天用墨色低透明度,黑夜里要更重才看得出层次。 */
const shadowSets = {
  light: {
    popup: "0 8px 24px rgba(31, 38, 48, 0.10), 0 1px 2px rgba(31, 38, 48, 0.06)",
    dialog: "0 24px 60px rgba(31, 38, 48, 0.16), 0 2px 6px rgba(31, 38, 48, 0.06)",
    tooltip: "0 4px 12px rgba(31, 38, 48, 0.18)",
    raised: "0 1px 2px rgba(31, 38, 48, 0.10)",
    thumb: "0 1px 2px rgba(31, 38, 48, 0.20)",
    backdrop: "rgba(31, 38, 48, 0.32)",
  },
  dark: {
    popup: "0 8px 24px rgba(0, 0, 0, 0.45), 0 1px 2px rgba(0, 0, 0, 0.30)",
    dialog: "0 24px 60px rgba(0, 0, 0, 0.55), 0 2px 6px rgba(0, 0, 0, 0.30)",
    tooltip: "0 4px 12px rgba(0, 0, 0, 0.45)",
    raised: "0 1px 2px rgba(0, 0, 0, 0.40)",
    thumb: "0 1px 2px rgba(0, 0, 0, 0.50)",
    backdrop: "rgba(0, 0, 0, 0.55)",
  },
} satisfies Record<ColorMode, Record<string, string>>;

const kebab = (name: string) => name.replace(/[A-Z]/g, (c) => `-${c.toLowerCase()}`);
const varName = (group: string, key: string) => `--cc-${group}-${kebab(key)}`;

/** 把一组令牌变成 `var(--cc-…)` 引用:换主题只需换 :root 上的值,组件不用重新取值。 */
function asVariables<T extends Record<string, string>>(group: string, source: T): { readonly [K in keyof T]: string } {
  return Object.fromEntries(Object.keys(source).map((key) => [key, `var(${varName(group, key)})`])) as {
    [K in keyof T]: string;
  };
}

/** 某个模式下所有令牌的 CSS 变量取值,由 MuiCssBaseline 挂到 :root。 */
export function colorVariables(mode: ColorMode): Record<string, string> {
  const vars: Record<string, string> = {};
  for (const [group, tokens] of Object.entries(palettes[mode])) {
    for (const [key, value] of Object.entries(tokens)) vars[varName(group, key)] = value;
  }
  for (const [key, value] of Object.entries(shadowSets[mode])) vars[varName("shadow", key)] = value;
  return vars;
}

// 下面这些是组件直接用的令牌,值是 CSS 变量引用。
// 需要具体色值的场合(MUI palette、Monaco、图表、SVG 属性)请用 palettes[mode]。
// 也不要对它们做字符串拼接(如 `${band.violet}66`):变量后面拼不出透明度,需要半透明就在色板里加一个带 alpha 的令牌。

/** 墨:四级无彩色文字。 */
export const ink = asVariables("ink", lightPalette.ink);
/** 地:云白与雾。 */
export const ground = asVariables("ground", lightPalette.ground);
/** 色带:只作 1–2px 边缘与浅底,不作文字。 */
export const band = asVariables("band", lightPalette.band);
/** 状态色:可作文字的深版本。 */
export const state = asVariables("state", lightPalette.state);
/** 控件专用色。 */
export const ui = asVariables("ui", lightPalette.ui);
/** 阴影与遮罩。 */
export const shadow = asVariables("shadow", shadowSets.light);

/** 顶栏下沿 / 焦点态 / 主按钮边缘的虹彩线:薄荷 → 紫罗兰 → 玫瑰,用色带原色,在底色上要看得见。 */
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
