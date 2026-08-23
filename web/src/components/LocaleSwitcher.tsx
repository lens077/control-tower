import { ToggleButton, ToggleButtonGroup } from "@mui/material";
import { useLocale } from "@/i18n";

export function LocaleSwitcher() {
  const { locale, setLocale } = useLocale();
  return (
    <ToggleButtonGroup
      exclusive
      size="small"
      value={locale}
      onChange={(_, next: "zh-CN" | "en" | null) => next && void setLocale(next)}
      aria-label="language"
    >
      <ToggleButton value="zh-CN">中</ToggleButton>
      <ToggleButton value="en">EN</ToggleButton>
    </ToggleButtonGroup>
  );
}
