import type { ReactNode } from "react";
import { Box, Typography } from "@mui/material";
import { hairline, ink, sp } from "@/styles/tokens";

/**
 * 全宽页面(连接 / Token / 系统)的统一骨架:
 * 标题行贴着顶栏,内容区自己滚动,最大宽度 1120。
 */
export function PageFrame({
  title,
  subtitle,
  actions,
  children,
  maxWidth = 1120,
}: {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
  children: ReactNode;
  maxWidth?: number;
}) {
  return (
    <Box sx={{ flex: 1, minHeight: 0, overflow: "auto" }}>
      <Box sx={{ maxWidth, mx: "auto", px: { xs: sp[4], md: sp[6] }, pt: sp[6], pb: sp[12] }}>
        <Box
          component="header"
          sx={{
            display: "flex",
            flexDirection: { xs: "column", sm: "row" },
            alignItems: { sm: "flex-end" },
            gap: sp[3],
            pb: sp[4],
            borderBottom: hairline,
          }}
        >
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Typography variant="h5" component="h1" sx={{ color: ink.strong }}>
              {title}
            </Typography>
            {subtitle && (
              <Typography sx={{ mt: sp[1], fontSize: 13, color: ink.muted, maxWidth: "70ch" }}>
                {subtitle}
              </Typography>
            )}
          </Box>
          {actions && <Box sx={{ display: "flex", alignItems: "center", gap: sp[2], flexWrap: "wrap" }}>{actions}</Box>}
        </Box>
        <Box sx={{ pt: sp[5], display: "flex", flexDirection: "column", gap: sp[5] }}>{children}</Box>
      </Box>
    </Box>
  );
}

/** 标签 + 值的小块,用于条目内的元信息。 */
export function Meta({ label, value, mono }: { label: string; value: ReactNode; mono?: boolean }) {
  return (
    <Box sx={{ minWidth: 0 }}>
      <Typography sx={{ fontSize: 11.5, color: ink.faint, lineHeight: 1.3 }}>{label}</Typography>
      <Typography
        component="div"
        sx={{
          fontSize: 13,
          color: ink.body,
          mt: "2px",
          fontFamily: mono ? "'JetBrains Mono Variable', ui-monospace, monospace" : undefined,
          fontVariantNumeric: "tabular-nums",
          overflowWrap: "anywhere",
        }}
      >
        {value}
      </Typography>
    </Box>
  );
}
