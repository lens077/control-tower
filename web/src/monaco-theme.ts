/**
 * Monaco 的「云白」配色:与页面同一套墨与色带,编辑器不再是页面里的一块异物。
 *
 * 通过 `<Editor beforeMount>` 注册;defineTheme 是幂等的,重复调用只是覆盖同名主题。
 */
import { band, ground, ink, state } from "@/styles/tokens";

export const CLOUD_THEME = "config-cloud";

type MonacoLike = {
  editor: {
    defineTheme: (name: string, theme: unknown) => void;
  };
};

export function defineCloudTheme(monaco: MonacoLike) {
  monaco.editor.defineTheme(CLOUD_THEME, {
    base: "vs",
    inherit: true,
    rules: [
      { token: "", foreground: ink.strong.slice(1) },
      { token: "comment", foreground: "8A93A0", fontStyle: "italic" },
      { token: "type", foreground: "2F6FB5" },
      { token: "keyword", foreground: "6B5BD6" },
      { token: "string", foreground: "1F8A6A" },
      { token: "string.yaml", foreground: "1F8A6A" },
      { token: "number", foreground: "C7384F" },
      { token: "number.yaml", foreground: "C7384F" },
      { token: "key", foreground: "3A434F" },
      { token: "type.yaml", foreground: "3A434F" },
      { token: "string.key.json", foreground: "3A434F" },
      { token: "string.value.json", foreground: "1F8A6A" },
      { token: "delimiter", foreground: "8A93A0" },
      { token: "operators", foreground: "8A93A0" },
      { token: "attribute.name", foreground: "3A434F" },
      { token: "attribute.value", foreground: "1F8A6A" },
    ],
    colors: {
      "editor.background": ground.cloud,
      "editor.foreground": ink.strong,
      "editorLineNumber.foreground": "#B4BAC4",
      "editorLineNumber.activeForeground": ink.muted,
      "editorCursor.foreground": state.active,
      "editor.selectionBackground": "#E3DEFF",
      "editor.inactiveSelectionBackground": "#EFEDFC",
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
      "scrollbarSlider.background": "#D3D8E080",
      "scrollbarSlider.hoverBackground": "#B9C0CAA0",
      "scrollbarSlider.activeBackground": "#B9C0CAC0",
      "scrollbar.shadow": "#00000000",
      "focusBorder": band.violet,
      "diffEditor.insertedTextBackground": "#BFEFE066",
      "diffEditor.insertedLineBackground": "#E6F7F1A0",
      "diffEditor.removedTextBackground": "#FFD6E680",
      "diffEditor.removedLineBackground": "#FCEBEFA0",
      "diffEditor.border": ground.line,
      "diffEditorGutter.insertedLineBackground": "#E6F7F1",
      "diffEditorGutter.removedLineBackground": "#FCEBEF",
      "minimap.background": ground.cloud,
    },
  });
}
