import { createTheme, type ThemeOptions } from "@mui/material/styles";
import type { Localization } from "@mui/material/locale";
import { band, font, grain, ground, ink, sheen, state } from "./tokens";

const popupShadow = "0 8px 24px rgba(31, 38, 48, 0.10), 0 1px 2px rgba(31, 38, 48, 0.06)";

// 云白工作台的 MUI 主题:平面、发丝边框、无彩正文,颜色只在边缘。
// 抽成常量是为了能带上语言包重建(见 createAppTheme)。
const themeOptions: ThemeOptions = {
  palette: {
    mode: "light",
    primary: { main: state.active, contrastText: "#FFFFFF" },
    secondary: { main: ink.muted },
    success: { main: state.success },
    warning: { main: state.warning },
    error: { main: state.danger },
    info: { main: state.info },
    divider: ground.line,
    background: { default: ground.cloud, paper: ground.cloud },
    text: { primary: ink.strong, secondary: ink.muted, disabled: "#A3ABB6" },
    action: {
      hover: "rgba(31, 38, 48, 0.04)",
      selected: state.activeSoft,
      focus: "rgba(107, 91, 214, 0.12)",
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
        "html, body": { height: "100%" },
        body: {
          fontFeatureSettings: '"tnum" 1',
          WebkitFontSmoothing: "antialiased",
          background: ground.cloud,
        },
        "::selection": { background: band.violet, color: ink.strong },
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
          background: "#B9C0CA",
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
          background: `linear-gradient(${band.violet}66, ${band.violet}66) padding-box, ${sheen} border-box`,
          backgroundColor: "#E4DEFF",
          "&:hover": {
            background: `linear-gradient(${band.violet}99, ${band.violet}99) padding-box, ${sheen} border-box`,
            backgroundColor: "#D6CEFF",
          },
          "&.Mui-disabled": {
            background: ground.mistDeep,
            color: "#A3ABB6",
            borderColor: ground.line,
          },
          "&.MuiButton-colorError": {
            color: state.danger,
            background: `linear-gradient(${state.dangerSoft}, ${state.dangerSoft}) padding-box, ${band.rose} border-box`,
            "&:hover": { background: `linear-gradient(#F9DCE3, #F9DCE3) padding-box, ${band.rose} border-box` },
          },
        },
        outlined: {
          borderColor: ground.lineStrong,
          color: ink.body,
          "&:hover": { borderColor: "#B9C0CA", background: ground.mist },
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
          "&:hover .MuiOutlinedInput-notchedOutline": { borderColor: "#B9C0CA" },
          // 聚焦:边框收成中性,颜色只出现在底沿的一条虹彩线上
          "&.Mui-focused": {
            backgroundImage: sheen,
            backgroundRepeat: "no-repeat",
            backgroundSize: "calc(100% - 12px) 2px",
            backgroundPosition: "center bottom 1px",
          },
          "&.Mui-focused .MuiOutlinedInput-notchedOutline": {
            borderColor: "#B9C0CA",
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
        paper: { boxShadow: popupShadow, borderRadius: 8, ...grain },
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
        paper: { boxShadow: popupShadow, borderRadius: 8, ...grain },
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
            borderColor: "#F4C4CD",
          },
          "&.MuiAlert-colorWarning": {
            background: state.warningSoft,
            color: state.warning,
            borderColor: "#EBD8AE",
          },
          "&.MuiAlert-colorInfo": { background: state.infoSoft, color: state.info, borderColor: "#C9DDF3" },
          "&.MuiAlert-colorSuccess": {
            background: state.successSoft,
            color: state.success,
            borderColor: "#BDE8D8",
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
          boxShadow: "0 24px 60px rgba(31, 38, 48, 0.16), 0 2px 6px rgba(31, 38, 48, 0.06)",
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
      styleOverrides: { root: { backgroundColor: "rgba(31, 38, 48, 0.32)" } },
    },
    MuiTooltip: {
      defaultProps: { arrow: false, enterDelay: 400 },
      styleOverrides: {
        tooltip: {
          background: ink.strong,
          boxShadow: "0 4px 12px rgba(31, 38, 48, 0.18)",
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
            boxShadow: "0 1px 2px rgba(31, 38, 48, 0.10)",
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
        thumb: { width: 16, height: 16, boxShadow: "0 1px 2px rgba(31,38,48,0.2)" },
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

/**
 * 按当前语言建主题。
 *
 * 第二个参数是 @mui/material/locale 的语言包,负责 MUI 内置组件的文案
 * (Autocomplete 的「无选项」、TablePagination 的「每页行数」之类)。
 */
export function createAppTheme(localization: Localization) {
  return createTheme(themeOptions, localization);
}
