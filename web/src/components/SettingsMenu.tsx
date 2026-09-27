import { useId, useState } from "react";
import { Box, IconButton, Popover, ToggleButton, ToggleButtonGroup, Tooltip, Typography } from "@mui/material";
import { Monitor, Moon, Settings, Sun } from "lucide-react";
import { useTranslation } from "@/i18n";
import { useColorMode, type ColorModePreference } from "@/styles/color-mode";
import { ink, sp } from "@/styles/tokens";

const OPTIONS: Array<{ value: ColorModePreference; icon: typeof Sun }> = [
  { value: "system", icon: Monitor },
  { value: "light", icon: Sun },
  { value: "dark", icon: Moon },
];

/** 顶栏的设置入口。目前只有「外观」一项:跟随系统(默认)/ 白天 / 黑夜。 */
export function SettingsMenu() {
  const { t } = useTranslation();
  const { preference, mode, setPreference } = useColorMode();
  const [anchor, setAnchor] = useState<HTMLElement | null>(null);
  const headingId = useId();

  return (
    <>
      <Tooltip title={t("settings.title")}>
        <IconButton
          aria-label={t("settings.title")}
          aria-haspopup="dialog"
          aria-expanded={anchor ? true : undefined}
          onClick={(event) => setAnchor(event.currentTarget)}
        >
          <Settings size={16} />
        </IconButton>
      </Tooltip>
      <Popover
        open={anchor !== null}
        anchorEl={anchor}
        onClose={() => setAnchor(null)}
        anchorOrigin={{ vertical: "bottom", horizontal: "right" }}
        transformOrigin={{ vertical: "top", horizontal: "right" }}
        slotProps={{ paper: { role: "dialog", "aria-label": t("settings.title"), sx: { mt: sp[1], p: sp[3], width: 320 } } }}
      >
        <Typography id={headingId} sx={{ fontSize: 12, fontWeight: 500, color: ink.muted, mb: sp[2] }}>
          {t("settings.appearance")}
        </Typography>
        <ToggleButtonGroup
          exclusive
          fullWidth
          size="small"
          value={preference}
          onChange={(_, next: ColorModePreference | null) => next && setPreference(next)}
          aria-labelledby={headingId}
        >
          {OPTIONS.map(({ value, icon: Icon }) => (
            <ToggleButton key={value} value={value} sx={{ gap: sp[1], whiteSpace: "nowrap" }}>
              <Icon size={14} aria-hidden="true" />
              {t(`settings.colorMode.${value}`)}
            </ToggleButton>
          ))}
        </ToggleButtonGroup>
        {preference === "system" && (
          <Box component="p" sx={{ m: 0, mt: sp[2], fontSize: 12, lineHeight: 1.45, color: ink.faint }}>
            {t("settings.systemHint", { mode: t(`settings.colorMode.${mode}`) })}
          </Box>
        )}
      </Popover>
    </>
  );
}
