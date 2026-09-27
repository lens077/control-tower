/**
 * Monaco 的「云白」配色:与页面同一套墨与色带,编辑器不再是页面里的一块异物。
 *
 * 白天、黑夜各一份,都通过 `<Editor beforeMount>` 注册;defineTheme 是幂等的,
 * 重复调用只是覆盖同名主题。切换模式时改 `theme` 属性即可,@monaco-editor/react
 * 会调用 setTheme。Monaco 解析不了 CSS 变量,这里必须用 palettes 里的具体色值。
 */
import type { ColorMode } from "@/styles/color-mode";
import { palettes } from "@/styles/tokens";

const THEME_NAMES: Record<ColorMode, string> = { light: "config-cloud", dark: "config-cloud-dark" };

export function cloudTheme(mode: ColorMode): string {
  return THEME_NAMES[mode];
}

type MonacoLike = {
  editor: {
    defineTheme: (name: string, theme: unknown) => void;
  };
};

/** 语法色与编辑器杂色。语法色沿用状态色,这里只放 palettes 里没有的几个编辑器专用值。 */
const EXTRA: Record<
  ColorMode,
  {
    comment: string;
    lineNumber: string;
    selection: string;
    scrollbar: [string, string, string];
    inserted: [text: string, line: string];
    removed: [text: string, line: string];
  }
> = {
  light: {
    comment: "#8A93A0",
    lineNumber: "#B4BAC4",
    selection: "#E3DEFF",
    scrollbar: ["#D3D8E080", "#B9C0CAA0", "#B9C0CAC0"],
    inserted: ["#BFEFE066", "#E6F7F1A0"],
    removed: ["#FFD6E680", "#FCEBEFA0"],
  },
  dark: {
    comment: "#7D8795",
    lineNumber: "#4F5865",
    selection: "#3A3268",
    scrollbar: ["#3A414C80", "#4F5865A0", "#4F5865C0"],
    inserted: ["#52C7A033", "#16302AA0"],
    removed: ["#F2839A33", "#3B1D26A0"],
  },
};

function themeData(mode: ColorMode) {
  const { ink, ground, band, state } = palettes[mode];
  const x = EXTRA[mode];
  const hex = (color: string) => color.slice(1);
  return {
    base: mode === "dark" ? "vs-dark" : "vs",
    inherit: true,
    rules: [
      { token: "", foreground: hex(ink.strong) },
      { token: "comment", foreground: hex(x.comment), fontStyle: "italic" },
      { token: "type", foreground: hex(state.info) },
      { token: "keyword", foreground: hex(state.active) },
      { token: "string", foreground: hex(state.success) },
      { token: "string.yaml", foreground: hex(state.success) },
      { token: "number", foreground: hex(state.danger) },
      { token: "number.yaml", foreground: hex(state.danger) },
      { token: "key", foreground: hex(ink.body) },
      { token: "type.yaml", foreground: hex(ink.body) },
      { token: "string.key.json", foreground: hex(ink.body) },
      { token: "string.value.json", foreground: hex(state.success) },
      { token: "delimiter", foreground: hex(x.comment) },
      { token: "operators", foreground: hex(x.comment) },
      { token: "attribute.name", foreground: hex(ink.body) },
      { token: "attribute.value", foreground: hex(state.success) },
    ],
    colors: {
      "editor.background": ground.cloud,
      "editor.foreground": ink.strong,
      "editorLineNumber.foreground": x.lineNumber,
      "editorLineNumber.activeForeground": ink.muted,
      "editorCursor.foreground": state.active,
      "editor.selectionBackground": x.selection,
      "editor.inactiveSelectionBackground": state.activeSoft,
      "editor.lineHighlightBackground": ground.mist,
      "editor.lineHighlightBorder": "#00000000",
      "editorIndentGuide.background1": ground.line,
      "editorIndentGuide.activeBackground1": ground.lineStrong,
      "editorWhitespace.foreground": ground.lineStrong,
      "editorGutter.background": ground.cloud,
      "editorError.foreground": state.danger,
      "editorWarning.foreground": state.warning,
      "editorWidget.background": ground.cloud,
      "editorWidget.border": ground.line,
      "editorSuggestWidget.background": ground.cloud,
      "editorSuggestWidget.border": ground.line,
      "editorSuggestWidget.selectedBackground": state.activeSoft,
      "editorHoverWidget.background": ground.cloud,
      "editorHoverWidget.border": ground.line,
      "scrollbarSlider.background": x.scrollbar[0],
      "scrollbarSlider.hoverBackground": x.scrollbar[1],
      "scrollbarSlider.activeBackground": x.scrollbar[2],
      "scrollbar.shadow": "#00000000",
      "focusBorder": band.violet,
      "diffEditor.insertedTextBackground": x.inserted[0],
      "diffEditor.insertedLineBackground": x.inserted[1],
      "diffEditor.removedTextBackground": x.removed[0],
      "diffEditor.removedLineBackground": x.removed[1],
      "diffEditor.border": ground.line,
      "diffEditorGutter.insertedLineBackground": state.successSoft,
      "diffEditorGutter.removedLineBackground": state.dangerSoft,
      "minimap.background": ground.cloud,
    },
  };
}

/** 一次注册白天与黑夜两份主题,之后切换模式只需改 theme 名。 */
export function defineCloudThemes(monaco: MonacoLike) {
  for (const mode of ["light", "dark"] as const) monaco.editor.defineTheme(THEME_NAMES[mode], themeData(mode));
}
