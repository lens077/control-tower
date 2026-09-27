import { createTheme, type ThemeOptions } from "@mui/material/styles";
import type { Localization } from "@mui/material/locale";
import type { ColorMode } from "./color-mode";
import { band, colorVariables, font, grain, ground, ink, palettes, shadow, sheen, state, ui } from "./tokens";

// 云白工作台的 MUI 主题:平面、发丝边框、无彩正文,颜色只在边缘。
// 写成函数是为了按白天/黑夜与语言包重建(见 createAppTheme)。
//
// palette 必须是具体色值:MUI 会拿它推算 light/dark/contrastText,解析不了 var()。
// 组件样式覆盖里用的是 CSS 变量,由下面 MuiCssBaseline 挂到 :root 的值决定。
const themeOptions = (mode: ColorMode): ThemeOptions => {
  const p = palettes[mode];
  return {
    palette: {
      mode,
      primary: { main: p.state.active, contrastText: p.ui.onPrimary },
      secondary: { main: p.ink.muted },
      success: { main: p.state.success },
      warning: { main: p.state.warning },
      error: { main: p.state.danger },
      info: { main: p.state.info },
      divider: p.ground.line,
      background: { default: p.ground.cloud, paper: p.ground.cloud },
      text: { primary: p.ink.strong, secondary: p.ink.muted, disabled: p.ui.textDisabled },
      action: {
        hover: mode === "dark" ? "rgba(255, 255, 255, 0.05)" : "rgba(31, 38, 48, 0.04)",
        selected: p.state.activeSoft,
        focus: mode === "dark" ? "rgba(169, 155, 250, 0.16)" : "rgba(107, 91, 214, 0.12)",
      },
    },
    shape: { borderRadius: 6 },
    typography: {
      fontFamily: font.sans,
      fontSize: 14,
      fontWeightLight: 300,
      fontWeightRegular: 400,
      fontWeightMedium: 500,
      fontWeightBold: 600,
      h5: { fontSize: 22, fontWeight: 300, letterSpacing: "-0.01em", lineHeight: 1.25 },
      h6: { fontSize: 17, fontWeight: 500, lineHeight: 1.3 },
      subtitle1: { fontSize: 15, fontWeight: 500, lineHeight: 1.4 },
      subtitle2: { fontSize: 13, fontWeight: 500, lineHeight: 1.4 },
      body1: { fontSize: 14, lineHeight: 1.5 },
      body2: { fontSize: 13, lineHeight: 1.45 },
      caption: { fontSize: 12, lineHeight: 1.4, color: ink.muted },
      button: { textTransform: "none", fontWeight: 500, fontSize: 13, letterSpacing: 0 },
    },
    components: {
      MuiCssBaseline: {
        styleOverrides: {
          // 当前模式的令牌取值;换模式时主题重建,这里跟着整体替换
          ":root": colorVariables(mode),
          // 让滚动条、表单控件、自动填充等浏览器原生部件也跟着深浅色走
          html: { colorScheme: mode },
          "html, body": { height: "100%" },
          body: {
            fontFeatureSettings: '"tnum" 1',
            WebkitFontSmoothing: "antialiased",
            background: ground.cloud,
          },
          "::selection": { background: ui.selection, color: ink.strong },
          "*": {
            caretColor: state.active,
            scrollbarWidth: "thin",
            scrollbarColor: `${ground.lineStrong} transparent`,
          },
          "*:focus-visible": {
            outline: `2px solid ${band.violet}`,
            outlineOffset: 1,
          },
          "*::-webkit-scrollbar": { width: 10, height: 10 },
          "*::-webkit-scrollbar-thumb": {
            background: ground.lineStrong,
            borderRadius: 8,
            border: "3px solid transparent",
            backgroundClip: "padding-box",
          },
          "*::-webkit-scrollbar-thumb:hover": {
            background: ui.lineHover,
            backgroundClip: "padding-box",
          },
          "*::-webkit-scrollbar-track": { background: "transparent" },
        },
      },
      MuiPaper: {
        defaultProps: { elevation: 0 },
        styleOverrides: {
          root: { backgroundImage: "none", border: `1px solid ${ground.line}` },
        },
      },
      MuiCard: {
        styleOverrides: {
          root: { boxShadow: "none", borderRadius: 8 },
        },
      },
      MuiButton: {
        defaultProps: { disableElevation: true, size: "small" },
        styleOverrides: {
          root: {
            minHeight: 30,
            padding: "4px 12px",
            borderRadius: 8,
            gap: 6,
            whiteSpace: "nowrap",
            "&.MuiButton-text.MuiButton-colorError": {
              color: state.danger,
              "&:hover": { background: state.dangerSoft },
            },
            "& .MuiButton-startIcon": { marginRight: 0, marginLeft: -2 },
            "& .MuiButton-startIcon > *:nth-of-type(1)": { fontSize: 16 },
          },
          // 主按钮:淡紫罗兰纸面 + 虹彩发丝边,深墨文字 —— 页面上唯一有填充的控件,
          // 但不是饱和色块。边框用双层背景画出渐变。
          contained: {
            color: ink.strong,
            border: "1px solid transparent",
            background: `linear-gradient(${ui.primaryVeil}, ${ui.primaryVeil}) padding-box, ${sheen} border-box`,
            backgroundColor: ui.primaryFill,
            "&:hover": {
              background: `linear-gradient(${ui.primaryVeilHover}, ${ui.primaryVeilHover}) padding-box, ${sheen} border-box`,
              backgroundColor: ui.primaryFillHover,
            },
            "&.Mui-disabled": {
              background: ground.mistDeep,
              color: ui.textDisabled,
              borderColor: ground.line,
            },
            "&.MuiButton-colorError": {
              color: state.danger,
              background: `linear-gradient(${state.dangerSoft}, ${state.dangerSoft}) padding-box, ${band.rose} border-box`,
              "&:hover": { background: `linear-gradient(${ui.dangerFillHover}, ${ui.dangerFillHover}) padding-box, ${band.rose} border-box` },
            },
          },
          outlined: {
            borderColor: ground.lineStrong,
            color: ink.body,
            "&:hover": { borderColor: ui.lineHover, background: ground.mist },
          },
          text: {
            color: ink.body,
            "&:hover": { background: ground.mist },
          },
          sizeSmall: { fontSize: 13 },
        },
      },
      MuiIconButton: {
        defaultProps: { size: "small" },
        styleOverrides: {
          root: {
            borderRadius: 6,
            color: ink.muted,
            "&:hover": { background: ground.mist, color: ink.strong },
          },
        },
      },
      MuiOutlinedInput: {
        styleOverrides: {
          root: {
            background: ground.cloud,
            borderRadius: 8,
            fontSize: 13,
            "& .MuiOutlinedInput-notchedOutline": {
              borderColor: ground.lineStrong,
              borderRadius: 8,
              transition: "border-color 120ms ease-out",
            },
            "&:hover .MuiOutlinedInput-notchedOutline": { borderColor: ui.lineHover },
            // 聚焦:边框收成中性,颜色只出现在底沿的一条虹彩线上
            "&.Mui-focused": {
              backgroundImage: sheen,
              backgroundRepeat: "no-repeat",
              backgroundSize: "calc(100% - 12px) 2px",
              backgroundPosition: "center bottom 1px",
            },
            "&.Mui-focused .MuiOutlinedInput-notchedOutline": {
              borderColor: ui.lineHover,
              borderWidth: 1,
            },
            "&.Mui-error .MuiOutlinedInput-notchedOutline": { borderColor: state.danger },
          },
          input: { padding: "7px 10px" },
          sizeSmall: { "& .MuiOutlinedInput-input": { padding: "5px 10px" } },
        },
      },
      MuiInputLabel: {
        styleOverrides: {
          root: {
            fontSize: 13,
            color: ink.muted,
            "&.Mui-focused": { color: state.active },
          },
        },
      },
      MuiFormHelperText: {
        styleOverrides: { root: { marginLeft: 2, fontSize: 12 } },
      },
      MuiSelect: {
        styleOverrides: { icon: { color: ink.muted } },
      },
      MuiMenu: {
        styleOverrides: {
          paper: { boxShadow: shadow.popup, borderRadius: 8, ...grain },
          list: { padding: 4 },
        },
      },
      MuiMenuItem: {
        styleOverrides: {
          root: {
            fontSize: 13,
            borderRadius: 4,
            minHeight: 30,
            "&.Mui-selected": { background: state.activeSoft },
            "&.Mui-selected:hover": { background: state.activeSoft },
          },
        },
      },
      MuiAutocomplete: {
        styleOverrides: {
          paper: { boxShadow: shadow.popup, borderRadius: 8, ...grain },
          listbox: {
            padding: 4,
            "& .MuiAutocomplete-option": { borderRadius: 4, minHeight: 30, fontSize: 13 },
          },
          option: { '&[aria-selected="true"]': { background: `${state.activeSoft} !important` } },
        },
      },
      MuiChip: {
        styleOverrides: {
          root: { height: 20, fontSize: 12, borderRadius: 4, fontWeight: 500 },
          label: { paddingLeft: 7, paddingRight: 7 },
          sizeSmall: { height: 20 },
          outlined: { borderColor: ground.lineStrong, color: ink.muted, background: ground.cloud },
          filled: { background: ground.mistDeep, color: ink.body },
          colorPrimary: { background: state.activeSoft, color: state.active },
          colorSuccess: { background: state.successSoft, color: state.success },
          colorWarning: { background: state.warningSoft, color: state.warning },
          colorError: { background: state.dangerSoft, color: state.danger },
          colorInfo: { background: state.infoSoft, color: state.info },
          icon: { marginLeft: 6, marginRight: -4, color: "inherit" },
        },
      },
      MuiAlert: {
        defaultProps: { variant: "standard" },
        styleOverrides: {
          root: {
            borderRadius: 6,
            fontSize: 13,
            padding: "4px 12px",
            alignItems: "center",
            border: "1px solid transparent",
          },
          standard: {
            "&.MuiAlert-colorError": {
              background: state.dangerSoft,
              color: state.danger,
              borderColor: ui.dangerBorder,
            },
            "&.MuiAlert-colorWarning": {
              background: state.warningSoft,
              color: state.warning,
              borderColor: ui.warningBorder,
            },
            "&.MuiAlert-colorInfo": { background: state.infoSoft, color: state.info, borderColor: ui.infoBorder },
            "&.MuiAlert-colorSuccess": {
              background: state.successSoft,
              color: state.success,
              borderColor: ui.successBorder,
            },
          },
          icon: { padding: "4px 0", marginRight: 10, opacity: 0.9 },
          message: { padding: "5px 0" },
          action: { paddingTop: 0 },
        },
      },
      MuiDialog: {
        styleOverrides: {
          paper: {
            borderRadius: 10,
            border: `1px solid ${ground.line}`,
            boxShadow: shadow.dialog,
            backgroundImage: `${sheen}, ${grain.backgroundImage}`,
            backgroundRepeat: "no-repeat, repeat",
            backgroundSize: `100% 2px, ${grain.backgroundSize}`,
            backgroundPosition: "top, 0 0",
          },
        },
      },
      MuiDialogTitle: {
        styleOverrides: { root: { fontSize: 16, fontWeight: 500, padding: "18px 20px 8px" } },
      },
      MuiDialogContent: {
        styleOverrides: { root: { padding: "8px 20px 12px" } },
      },
      MuiDialogActions: {
        styleOverrides: { root: { padding: "8px 20px 16px", gap: 6 } },
      },
      MuiBackdrop: {
        // 只给对话框的遮罩上色;Menu/Popover/Select 用的是 invisible 遮罩,不能把页面压暗
        styleOverrides: {
          root: { backgroundColor: shadow.backdrop },
          invisible: { backgroundColor: "transparent" },
        },
      },
      MuiTooltip: {
        defaultProps: { arrow: false, enterDelay: 400 },
        styleOverrides: {
          tooltip: {
            background: ui.tooltip,
            color: ui.tooltipText,
            boxShadow: shadow.tooltip,
            fontSize: 12,
            fontWeight: 400,
            padding: "5px 8px",
            borderRadius: 4,
          },
        },
      },
      MuiToggleButtonGroup: {
        styleOverrides: {
          root: { background: ground.mist, borderRadius: 6, padding: 2, gap: 2 },
          grouped: {
            border: "none !important",
            borderRadius: "4px !important",
            margin: 0,
          },
        },
      },
      MuiToggleButton: {
        styleOverrides: {
          root: {
            textTransform: "none",
            fontSize: 12,
            fontWeight: 500,
            padding: "3px 10px",
            minHeight: 26,
            color: ink.muted,
            "&:hover": { background: ground.mistDeep },
            "&.Mui-selected": {
              background: ground.cloud,
              color: ink.strong,
              boxShadow: shadow.raised,
            },
            "&.Mui-selected:hover": { background: ground.cloud },
          },
        },
      },
      MuiSwitch: {
        styleOverrides: {
          root: { width: 34, height: 20, padding: 0, marginRight: 8 },
          switchBase: {
            padding: 2,
            "&.Mui-checked": { transform: "translateX(14px)", color: "#fff" },
            "&.Mui-checked + .MuiSwitch-track": { background: state.active, opacity: 1 },
          },
          thumb: { width: 16, height: 16, boxShadow: shadow.thumb },
          track: { borderRadius: 10, background: ground.lineStrong, opacity: 1 },
        },
      },
      MuiFormControlLabel: {
        styleOverrides: { root: { marginLeft: 0, marginRight: 0 }, label: { fontSize: 13 } },
      },
      MuiDivider: { styleOverrides: { root: { borderColor: ground.line } } },
      MuiLinearProgress: {
        styleOverrides: {
          root: { background: ground.mistDeep, borderRadius: 2, height: 4 },
          bar: { borderRadius: 2 },
        },
      },
      MuiCircularProgress: {
        defaultProps: { thickness: 3 },
      },
      MuiTableCell: {
        styleOverrides: {
          root: { borderBottom: `1px solid ${ground.line}`, padding: "6px 12px", fontSize: 13 },
          head: {
            color: ink.faint,
            fontSize: 12,
            fontWeight: 500,
            padding: "6px 12px",
            background: ground.mist,
            whiteSpace: "nowrap",
          },
        },
      },
      MuiTableRow: {
        styleOverrides: {
          root: { "&:last-child td": { borderBottom: 0 } },
        },
      },
      MuiListItemButton: {
        styleOverrides: {
          root: { borderRadius: 4, "&.Mui-selected": { background: state.activeSoft } },
        },
      },
    },
  };
};

/**
 * 按配色模式与当前语言建主题。
 *
 * 第二个参数是 @mui/material/locale 的语言包,负责 MUI 内置组件的文案
 * (Autocomplete 的「无选项」、TablePagination 的「每页行数」之类)。
 */
export function createAppTheme(mode: ColorMode, localization: Localization) {
  return createTheme(themeOptions(mode), localization);
}
