import { useState } from "react";
import {
  Box,
  Button,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  MenuItem,
  TextField,
  Typography,
} from "@mui/material";
import { useSnapshot } from "valtio";
import { ConfigFormat } from "@/api";
import { useTranslation } from "@/i18n";
import { FORMAT_OPTIONS, formatLabel } from "@/lib/format";
import { editorStore } from "@/store/editor";
import { font, ink, sp } from "@/styles/tokens";
import { EnvBand } from "./Explorer";

export function NewKeyDialog({
  open,
  onClose,
  onCreate,
  existing,
}: {
  open: boolean;
  onClose: () => void;
  onCreate: (key: string) => void;
  /** 当前位置已有的 key,用来在输入时提示「已存在,会打开它而不是新建」 */
  existing: readonly string[];
}) {
  const { t } = useTranslation();
  const snap = useSnapshot(editorStore);
  const [key, setKey] = useState("");
  const [format, setFormat] = useState<ConfigFormat>(ConfigFormat.YAML);
  const trimmed = key.trim();
  const exists = trimmed !== "" && existing.includes(trimmed);

  return (
    <Dialog
      open={open}
      onClose={onClose}
      slotProps={{ paper: { sx: { width: 460 } }, transition: { onExited: () => setKey("") } }}
    >
      <Box
        component="form"
        onSubmit={(e) => {
          e.preventDefault();
          if (trimmed) onCreate(trimmed);
        }}
      >
        <DialogTitle>{t("newKey.title")}</DialogTitle>
        <DialogContent>
          <Typography
            sx={{ display: "flex", alignItems: "center", gap: sp[1], fontSize: 12.5, color: ink.muted, mb: sp[3] }}
          >
            {t("newKey.location")}
            <Box component="span" sx={{ fontFamily: font.mono, color: ink.body }}>
              {snap.namespace || "—"}
            </Box>
            <Box component="span" sx={{ color: ink.faint }}>
              ›
            </Box>
            <EnvBand env={snap.environment} height={12} />
            <Box component="span" sx={{ fontFamily: font.mono, color: ink.body }}>
              {snap.environment || "—"}
            </Box>
          </Typography>
          <Box sx={{ display: "flex", flexDirection: "column", gap: sp[3] }}>
            <TextField
              label={t("newKey.keyLabel")}
              value={key}
              onChange={(e) => setKey(e.target.value)}
              fullWidth
              autoFocus
              helperText={exists ? t("newKey.exists") : t("newKey.keyHelp")}
              slotProps={{ htmlInput: { style: { fontFamily: font.mono }, spellCheck: false } }}
            />
            <TextField
              select
              label={t("newKey.format")}
              value={format}
              onChange={(e) => setFormat(Number(e.target.value) as ConfigFormat)}
            >
              {FORMAT_OPTIONS.map((f) => (
                <MenuItem key={f} value={f}>
                  {formatLabel(f)}
                </MenuItem>
              ))}
            </TextField>
          </Box>
        </DialogContent>
        <DialogActions>
          <Button onClick={onClose}>{t("action.cancel")}</Button>
          <Button type="submit" variant="contained" disabled={!trimmed}>
            {exists ? t("newKey.open") : t("newKey.submit")}
          </Button>
        </DialogActions>
      </Box>
    </Dialog>
  );
}
