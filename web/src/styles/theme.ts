import { createTheme, type ThemeOptions } from "@mui/material/styles";
import type { Localization } from "@mui/material/locale";
import { glassPanel } from "./glass";

// 玻璃态 MUI 主题:半透明面板 + 背景模糊 + 发丝边框。
// 抽成常量是为了能带上语言包重建(见 createAppTheme)。
const themeOptions: ThemeOptions = {
  palette: {
    mode: "light",
    primary: { main: "#3b6cf6" },
    secondary: { main: "#a855f7" },
    background: {
      default: "transparent", // 背景由 body 渐变提供
      paper: "rgba(255,255,255,0.6)",
    },
    text: {
      primary: "#111827",
      secondary: "#4b5563",
    },
  },
  shape: { borderRadius: 14 },
  typography: {
    fontFamily: [
      "Roboto",
      "-apple-system",
      "BlinkMacSystemFont",
      "PingFang SC",
      "Segoe UI",
      "sans-serif",
    ].join(","),
  },
  components: {
    MuiPaper: {
      styleOverrides: {
        root: {
          ...glassPanel,
          backgroundImage: "none",
        },
      },
    },
    MuiCard: {
      styleOverrides: {
        root: { ...glassPanel },
      },
    },
    MuiAppBar: {
      styleOverrides: {
        root: {
          background: "rgba(255,255,255,0.55)",
          backdropFilter: "blur(20px) saturate(180%)",
          WebkitBackdropFilter: "blur(20px) saturate(180%)",
          borderBottom: "1px solid rgba(255,255,255,0.35)",
          boxShadow: "0 4px 20px rgba(31,38,135,0.08)",
          color: "#111827",
        },
      },
      defaultProps: { elevation: 0 },
    },
    MuiDrawer: {
      styleOverrides: {
        paper: {
          background: "rgba(255,255,255,0.5)",
          backdropFilter: "blur(20px) saturate(180%)",
          WebkitBackdropFilter: "blur(20px) saturate(180%)",
          borderRight: "1px solid rgba(255,255,255,0.35)",
        },
      },
    },
    MuiButton: {
      styleOverrides: {
        root: { textTransform: "none", borderRadius: 12 },
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
