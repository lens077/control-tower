import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useSnapshot } from "valtio";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  Autocomplete,
  Box,
  Button,
  Card,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  IconButton,
  List,
  ListItemButton,
  ListItemText,
  MenuItem,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import { KeyRound, Plus, RefreshCw } from "lucide-react";
import { toAppError } from "@/api/transport";
import { useTranslation } from "@/i18n";
import { configApi, ConfigFormat } from "@/api";
import { useAuthState } from "@/providers/AuthProvider";
import { editorStore, setEnvironment, setNamespace } from "@/store/editor";

import { ENV_OPTIONS, FORMAT_OPTIONS, formatLabel } from "@/lib/format";
import { sp } from "@/styles/glass";

export const Route = createFileRoute("/")({
  component: BrowserPage,
});

function BrowserPage() {
  const { t } = useTranslation();
  const snap = useSnapshot(editorStore);
  const { isAuthenticated } = useAuthState();
  const navigate = useNavigate();
  const [prefix, setPrefix] = useState("");
  const [newOpen, setNewOpen] = useState(false);

  // 配置中心里真实存在的 namespace / environment,用于下拉选择
  const { data: nsData, refetch: refetchNamespaces } = useQuery({
    queryKey: ["listNamespaces"],
    queryFn: ({ signal }) => configApi.listNamespaces(signal),
    enabled: isAuthenticated,
  });

  const namespaces = useMemo(() => nsData?.namespaces ?? [], [nsData]);
  const nsOptions = useMemo(() => namespaces.map((n) => n.namespace), [namespaces]);

  // 环境选项:当前 namespace 下确实有配置的环境优先,其余标准环境仍可选(用于新建)
  const envOptions = useMemo(() => {
    const existing = namespaces.find((n) => n.namespace === snap.namespace)?.environments ?? [];
    return [...new Set([...existing, ...ENV_OPTIONS])];
  }, [namespaces, snap.namespace]);

  // 仅在 namespace 列表首次到达时纠正一次:本地存的 namespace 若已不存在(或为空),
  // 落到第一个真实 namespace,避免停在空 namespace 上误以为「刷新不出配置」。
  // 只跑一次是必须的 —— 否则用户手输一个尚不存在的新 namespace 时会被这里改回去。
  const alignedRef = useRef(false);
  useEffect(() => {
    if (alignedRef.current || namespaces.length === 0) return;
    alignedRef.current = true;

    const { namespace, environment } = editorStore;
    if (namespace && namespaces.some((n) => n.namespace === namespace)) return;

    const first = namespaces[0];
    setNamespace(first.namespace);
    if (first.environments.length > 0 && !first.environments.includes(environment)) {
      setEnvironment(first.environments[0]);
    }
  }, [namespaces]);

  const { data, isLoading, isError, error, refetch, isFetching } = useQuery({
    queryKey: ["listKeys", snap.namespace, snap.environment, prefix],
    queryFn: ({ signal }) => configApi.listKeys(snap.namespace, snap.environment, prefix, signal),
    // 仅在已认证时发起，避免未认证/坏 token 触发无谓的 401 与退登循环
    // namespace 为空时后端会因 required 校验报错，等下拉填好再查
    enabled: isAuthenticated && snap.namespace !== "",
  });

  // 当前 namespace 下「其它有配置的环境」,用于空列表时提示选错了环境
  const otherEnvs = useMemo(
    () =>
      (namespaces.find((n) => n.namespace === snap.namespace)?.environments ?? []).filter(
        (e) => e !== snap.environment,
      ),
    [namespaces, snap.namespace, snap.environment],
  );

  const openKey = (key: string) => {
    navigate({ to: "/edit", search: { ns: snap.namespace, env: snap.environment, key } });
  };

  return (
    <Box sx={{ maxWidth: 1080, mx: "auto", display: "flex", flexDirection: "column", gap: sp[4] }}>
      {/* 顶部:命名空间 / 环境 / 搜索 */}
      <Card sx={{ p: sp[4] }}>
        <Box sx={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: sp[3] }}>
          {/* freeSolo:既能从已有 namespace 里选,也能直接输入一个新的用于建首个 key */}
          <Autocomplete
            freeSolo
            options={nsOptions}
            value={snap.namespace}
            onChange={(_, v) => setNamespace(v ?? "")}
            onInputChange={(_, v) => setNamespace(v)}
            sx={{ minWidth: 200 }}
            renderOption={(props, option) => {
              const info = namespaces.find((n) => n.namespace === option);
              return (
                <Box component="li" {...props} key={option}>
                  <Box sx={{ flex: 1 }}>{option}</Box>
                  {info && <Chip label={`${info.keyCount} keys`} size="small" variant="outlined" />}
                </Box>
              );
            }}
            renderInput={(params) => (
              <TextField {...params} label={t("browser.namespace")} size="small" />
            )}
          />
          <Autocomplete
            freeSolo
            options={envOptions}
            value={snap.environment}
            onChange={(_, v) => setEnvironment(v ?? "")}
            onInputChange={(_, v) => setEnvironment(v)}
            sx={{ minWidth: 140 }}
            renderInput={(params) => (
              <TextField {...params} label={t("browser.environment")} size="small" />
            )}
          />
          <TextField
            label={t("browser.prefixFilter")}
            size="small"
            value={prefix}
            onChange={(e) => setPrefix(e.target.value)}
            sx={{ flex: 1, minWidth: 200 }}
          />
          <Tooltip title={t("common:action.refresh")}>
            <IconButton
              onClick={() => {
                void refetchNamespaces();
                void refetch();
              }}
            >
              <RefreshCw size={18} />
            </IconButton>
          </Tooltip>
          <Button
            variant="contained"
            startIcon={<Plus size={18} />}
            onClick={() => setNewOpen(true)}
          >
            {t("browser.newKey")}
          </Button>
        </Box>
      </Card>

      {/* key 列表 */}
      <Card>
        <Box sx={{ p: sp[4], pb: sp[2] }}>
          <Typography variant="subtitle1" sx={{ fontWeight: 700 }}>
            {snap.namespace} / {snap.environment}
            {isFetching && <CircularProgress size={14} sx={{ ml: sp[2] }} />}
          </Typography>
        </Box>
        <Divider />
        {isLoading ? (
          <Box sx={{ p: sp[6], textAlign: "center" }}>
            <CircularProgress />
          </Box>
        ) : isError ? (
          <Box sx={{ p: sp[4] }}>
            <Typography color="error">
              {t("browser.loadFailed", { message: toAppError(error).message })}
            </Typography>
          </Box>
        ) : !data || data.entries.length === 0 ? (
          <Box sx={{ p: sp[6], textAlign: "center" }}>
            <Typography color="text.secondary">
              {!snap.namespace
                ? t("browser.emptyNoNamespace")
                : otherEnvs.length > 0
                  ? t("browser.emptyOtherEnvs", {
                      namespace: snap.namespace,
                      environment: snap.environment,
                      // 顿号在英文里要换成逗号,分隔符跟着语言走
                      envs: otherEnvs.join(t("browser.envJoin")),
                    })
                  : t("browser.empty")}
            </Typography>
          </Box>
        ) : (
          <List disablePadding>
            {data.entries.map((e) => (
              <ListItemButton key={e.key} onClick={() => openKey(e.key)} sx={{ px: sp[4] }}>
                <KeyRound size={16} style={{ marginRight: 12, opacity: 0.6 }} />
                <ListItemText
                  primary={
                    <Box component="span" sx={{ fontFamily: "monospace" }}>
                      {e.key}
                    </Box>
                  }
                  secondary={`v${e.version} · ${e.updatedBy || "—"}`}
                />
                <Box sx={{ display: "flex", alignItems: "center", gap: sp[1] }}>
                  {e.isSecret && (
                    <Chip label="secret" size="small" color="warning" variant="outlined" />
                  )}
                  <Chip label={formatLabel(e.format)} size="small" variant="outlined" />
                </Box>
              </ListItemButton>
            ))}
          </List>
        )}
      </Card>

      <NewKeyDialog
        open={newOpen}
        onClose={() => setNewOpen(false)}
        onCreate={(key) => {
          setNewOpen(false);
          openKey(key);
        }}
      />
    </Box>
  );
}

function NewKeyDialog({
  open,
  onClose,
  onCreate,
}: {
  open: boolean;
  onClose: () => void;
  onCreate: (key: string) => void;
}) {
  const { t } = useTranslation();
  const [key, setKey] = useState("");
  const [format, setFormat] = useState<ConfigFormat>(ConfigFormat.YAML);

  return (
    <Dialog open={open} onClose={onClose} slotProps={{ paper: { sx: { minWidth: 420 } } }}>
      <DialogTitle>{t("newKey.title")}</DialogTitle>
      <DialogContent>
        <Box sx={{ display: "flex", flexDirection: "column", gap: sp[3], mt: sp[1] }}>
          <TextField
            label={t("newKey.keyLabel")}
            value={key}
            onChange={(e) => setKey(e.target.value)}
            fullWidth
            autoFocus
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
        <Button onClick={onClose}>{t("common:action.cancel")}</Button>
        <Button variant="contained" disabled={!key.trim()} onClick={() => onCreate(key.trim())}>
          {t("newKey.submit")}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
