import { useNavigate, useRouterState } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useSnapshot } from "valtio";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  Autocomplete,
  Box,
  ButtonBase,
  IconButton,
  InputAdornment,
  Skeleton,
  TextField,
  Tooltip,
  Typography,
  useMediaQuery,
  useTheme,
} from "@mui/material";
import { ChevronDown, ChevronRight, Lock, Plus, RefreshCw, Search, X } from "lucide-react";
import { toAppError } from "@/api/transport";
import { useTranslation } from "@/i18n";
import { configApi } from "@/api";
import type { ConfigEntry } from "@/gen/api";
import { useAuthState } from "@/providers/AuthProvider";
import { editorStore, setEnvironment, setNamespace } from "@/store/editor";
import { ENV_OPTIONS, formatLabel } from "@/lib/format";
import { fmtRelative } from "@/lib/time";
import { band, envTone, font, grain, ground, hairline, ink, sheen, sp, state } from "@/styles/tokens";
import { NewKeyDialog } from "./NewKeyDialog";

export const EXPLORER_WIDTH = 288;

/** 按目录分组:`gateway/routes.yaml` → 组 `gateway/`,名 `routes.yaml`;根级 key 不分组。 */
interface KeyGroup {
  dir: string;
  entries: Array<{ entry: ConfigEntry; name: string }>;
}

function groupByDir(entries: readonly ConfigEntry[]): KeyGroup[] {
  const map = new Map<string, KeyGroup>();
  for (const entry of entries) {
    const idx = entry.key.lastIndexOf("/");
    const dir = idx >= 0 ? entry.key.slice(0, idx + 1) : "";
    const name = idx >= 0 ? entry.key.slice(idx + 1) : entry.key;
    let group = map.get(dir);
    if (!group) {
      group = { dir, entries: [] };
      map.set(dir, group);
    }
    group.entries.push({ entry, name });
  }
  // 根级 key 排最前,其余按目录名
  return [...map.values()].sort((a, b) => (a.dir === "" ? -1 : b.dir === "" ? 1 : a.dir.localeCompare(b.dir)));
}

export function Explorer() {
  const { t } = useTranslation();
  const snap = useSnapshot(editorStore);
  const { isAuthenticated } = useAuthState();
  const navigate = useNavigate();
  const location = useRouterState({ select: (s) => s.location });
  const [filter, setFilter] = useState("");
  const [newOpen, setNewOpen] = useState(false);
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  // 窄屏 + 正在看某个 key 时,列表默认收起,把屏幕让给编辑器
  const narrow = useMediaQuery(useTheme().breakpoints.down("md"));
  const [listOpen, setListOpen] = useState(false);

  const onKeyRoute = location.pathname.startsWith("/edit") || location.pathname.startsWith("/history");
  const listCollapsed = narrow && onKeyRoute && !listOpen;
  const currentKey = onKeyRoute ? ((location.search as { key?: string }).key ?? null) : null;

  // 配置中心里真实存在的 namespace / environment,用于下拉选择
  const { data: nsData, refetch: refetchNamespaces } = useQuery({
    queryKey: ["listNamespaces"],
    queryFn: ({ signal }) => configApi.listNamespaces(signal),
    enabled: isAuthenticated,
  });

  const namespaces = useMemo(() => nsData?.namespaces ?? [], [nsData]);
  const nsOptions = useMemo(() => namespaces.map((n) => n.namespace), [namespaces]);
  const nsInfo = namespaces.find((n) => n.namespace === snap.namespace);

  // 环境选项:当前 namespace 下确实有配置的环境优先,其余标准环境仍可选(用于新建)
  const envOptions = useMemo(() => {
    const existing = nsInfo?.environments ?? [];
    return [...new Set([...existing, ...ENV_OPTIONS])];
  }, [nsInfo]);

  // 仅在 namespace 列表首次到达时纠正一次:本地存的 namespace 若已不存在(或为空),
  // 落到第一个真实 namespace,避免停在空 namespace 上误以为「刷新不出配置」。
  // 只跑一次是必须的 —— 否则用户手输一个尚不存在的新 namespace 时会被这里改回去。
  const alignedRef = useRef(false);
  useEffect(() => {
    if (alignedRef.current || namespaces.length === 0) return;
    alignedRef.current = true;
    // 深链打开的编辑/历史页以 URL 为准(可能是一个尚不存在的新 namespace),这里不纠正
    if (onKeyRoute) return;
    const { namespace, environment } = editorStore;
    if (namespace && namespaces.some((n) => n.namespace === namespace)) return;
    const first = namespaces[0];
    setNamespace(first.namespace);
    if (first.environments.length > 0 && !first.environments.includes(environment)) {
      setEnvironment(first.environments[0]);
    }
  }, [namespaces, onKeyRoute]);

  const keys = useQuery({
    queryKey: ["listKeys", snap.namespace, snap.environment, ""],
    queryFn: ({ signal }) => configApi.listKeys(snap.namespace, snap.environment, "", signal),
    // 仅在已认证时发起,避免未认证/坏 token 触发无谓的 401 与退登循环
    // namespace 为空时后端会因 required 校验报错,等下拉填好再查
    enabled: isAuthenticated && snap.namespace !== "",
  });

  const entries = keys.data?.entries ?? [];
  const needle = filter.trim().toLowerCase();
  const visible = needle ? entries.filter((e) => e.key.toLowerCase().includes(needle)) : entries;
  const groups = useMemo(() => groupByDir(visible), [visible]);

  // 当前 namespace 下「其它有配置的环境」,用于空列表时提示选错了环境
  const otherEnvs = (nsInfo?.environments ?? []).filter((e) => e !== snap.environment);

  const openKey = (key: string) => {
    navigate({ to: "/edit", search: { ns: snap.namespace, env: snap.environment, key } });
  };

  // 换了 namespace / environment 后,编辑页里的 key 已经不属于这个位置,退回总览
  const changeLocation = (ns: string, env: string) => {
    setNamespace(ns);
    setEnvironment(env);
    if (onKeyRoute) navigate({ to: "/" });
  };

  return (
    <Box
      component="aside"
      aria-label={t("explorer.aria")}
      sx={{
        width: { xs: "100%", md: EXPLORER_WIDTH },
        flexShrink: 0,
        borderRight: { md: hairline },
        borderBottom: { xs: hairline, md: "none" },
        bgcolor: ground.mist,
        ...grain,
        display: "flex",
        flexDirection: "column",
        minHeight: 0,
        maxHeight: { xs: "40vh", md: "none" },
      }}
    >
      {/* 位置:命名空间 / 环境。标签固定在左侧,不用浮动标签 —— 两行就是一张「你在哪」的属性表 */}
      <Box
        sx={{
          p: sp[3],
          pb: sp[2],
          display: "grid",
          gridTemplateColumns: "auto 1fr",
          alignItems: "center",
          columnGap: sp[2],
          rowGap: sp[1],
        }}
      >
        <FieldLabel htmlFor="explorer-namespace">{t("browser.namespace")}</FieldLabel>
        {/* freeSolo:既能从已有 namespace 里选,也能直接输入一个新的用于建首个 key */}
        <Autocomplete
          id="explorer-namespace"
          freeSolo
          size="small"
          options={nsOptions}
          value={snap.namespace}
          onChange={(_, v) => changeLocation(v ?? "", snap.environment)}
          onInputChange={(_, v, reason) => {
            if (reason === "input" || reason === "clear") changeLocation(v, snap.environment);
          }}
          renderOption={(props, option) => {
            const info = namespaces.find((n) => n.namespace === option);
            const { key, ...rest } = props as typeof props & { key: string };
            return (
              <Box component="li" key={key} {...rest} sx={{ gap: sp[2] }}>
                <Box sx={{ flex: 1, fontFamily: font.mono, fontSize: 12.5 }}>{option}</Box>
                {info && (
                  <Typography sx={{ fontSize: 11, color: ink.faint, fontVariantNumeric: "tabular-nums" }}>
                    {t("explorer.keyCount", { count: info.keyCount })}
                  </Typography>
                )}
              </Box>
            );
          }}
          renderInput={(params) => (
            <TextField
              {...params}
              placeholder={t("explorer.namespacePlaceholder")}
              slotProps={{
                ...params.slotProps,
                htmlInput: { ...params.slotProps.htmlInput, style: { fontFamily: font.mono, fontSize: 12.5 } },
              }}
            />
          )}
        />
        <FieldLabel htmlFor="explorer-environment">{t("browser.environment")}</FieldLabel>
        <Autocomplete
          id="explorer-environment"
          freeSolo
          size="small"
          options={envOptions}
          value={snap.environment}
          onChange={(_, v) => changeLocation(snap.namespace, v ?? "")}
          onInputChange={(_, v, reason) => {
            if (reason === "input" || reason === "clear") changeLocation(snap.namespace, v);
          }}
          renderOption={(props, option) => {
            const has = nsInfo?.environments.includes(option);
            const { key, ...rest } = props as typeof props & { key: string };
            return (
              <Box component="li" key={key} {...rest} sx={{ gap: sp[2] }}>
                <EnvBand env={option} />
                <Box sx={{ flex: 1, fontFamily: font.mono, fontSize: 12.5 }}>{option}</Box>
                {!has && (
                  <Typography sx={{ fontSize: 11, color: ink.faint }}>{t("explorer.envEmpty")}</Typography>
                )}
              </Box>
            );
          }}
          renderInput={(params) => (
            <TextField
              {...params}
              slotProps={{
                ...params.slotProps,
                htmlInput: { ...params.slotProps.htmlInput, style: { fontFamily: font.mono, fontSize: 12.5 } },
                input: {
                  ...params.slotProps.input,
                  startAdornment: (
                    <InputAdornment position="start" sx={{ ml: "2px", mr: "-2px" }}>
                      <EnvBand env={snap.environment} />
                    </InputAdornment>
                  ),
                },
              }}
            />
          )}
        />
      </Box>

      {/* 过滤 / 刷新 / 新建 */}
      <Box sx={{ px: sp[3], pb: sp[2], display: "flex", gap: sp[1], alignItems: "center" }}>
        <TextField
          size="small"
          placeholder={t("explorer.filter")}
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          sx={{ flex: 1 }}
          slotProps={{
            htmlInput: { "aria-label": t("explorer.filter") },
            input: {
              startAdornment: (
                <InputAdornment position="start" sx={{ mr: "4px", color: ink.faint }}>
                  <Search size={14} />
                </InputAdornment>
              ),
              endAdornment: filter ? (
                <InputAdornment position="end" sx={{ mr: "-4px" }}>
                  <IconButton aria-label={t("explorer.clearFilter")} onClick={() => setFilter("")} sx={{ p: "2px" }}>
                    <X size={13} />
                  </IconButton>
                </InputAdornment>
              ) : undefined,
            },
          }}
        />
        <Tooltip title={t("action.refresh")}>
          <span>
            <IconButton
              aria-label={t("action.refresh")}
              disabled={keys.isFetching}
              onClick={() => {
                void refetchNamespaces();
                void keys.refetch();
              }}
            >
              <RefreshCw size={15} />
            </IconButton>
          </span>
        </Tooltip>
        <Tooltip title={t("browser.newKey")}>
          <IconButton
            aria-label={t("browser.newKey")}
            onClick={() => setNewOpen(true)}
            sx={{
              color: ink.strong,
              border: "1px solid transparent",
              background: `linear-gradient(${band.violet}66, ${band.violet}66) padding-box, ${sheen} border-box`,
              "&:hover": {
                color: ink.strong,
                background: `linear-gradient(${band.violet}99, ${band.violet}99) padding-box, ${sheen} border-box`,
              },
            }}
          >
            <Plus size={15} />
          </IconButton>
        </Tooltip>
      </Box>

      {/* 列表标题:位置 + 计数;窄屏看 key 时可以收起整张列表 */}
      <Box
        component={narrow && onKeyRoute ? ButtonBase : "div"}
        onClick={narrow && onKeyRoute ? () => setListOpen((v) => !v) : undefined}
        aria-expanded={narrow && onKeyRoute ? !listCollapsed : undefined}
        sx={{
          width: "100%",
          px: sp[3],
          py: sp[1],
          display: "flex",
          alignItems: "center",
          gap: sp[2],
          borderTop: hairline,
          borderBottom: listCollapsed ? "none" : hairline,
          bgcolor: ground.cloud,
          minHeight: 28,
          textAlign: "left",
        }}
      >
        <Typography
          sx={{
            fontSize: 11,
            fontWeight: 500,
            letterSpacing: "0.06em",
            textTransform: "uppercase",
            color: ink.faint,
          }}
        >
          {t("explorer.keys")}
        </Typography>
        <Box sx={{ flex: 1 }} />
        {keys.data && (
          <Typography sx={{ fontSize: 11, color: ink.faint, fontVariantNumeric: "tabular-nums" }}>
            {needle ? `${visible.length} / ${entries.length}` : entries.length}
          </Typography>
        )}
        {narrow && onKeyRoute && (
          <ChevronDown
            size={14}
            color={ink.faint}
            style={{ transform: listCollapsed ? "rotate(-90deg)" : "none", transition: "transform 150ms ease-out" }}
          />
        )}
      </Box>

      {/* key 列表 */}
      <Box
        sx={{
          flex: 1,
          minHeight: 0,
          overflow: "auto",
          bgcolor: ground.cloud,
          pb: sp[4],
          display: listCollapsed ? "none" : "block",
        }}
      >
        {!snap.namespace ? (
          <Hint>{t("browser.emptyNoNamespace")}</Hint>
        ) : keys.isLoading ? (
          <Box sx={{ px: sp[3], pt: sp[2], display: "grid", gap: sp[2] }}>
            {[0.7, 0.55, 0.8, 0.45, 0.65].map((w, i) => (
              <Skeleton key={i} variant="text" width={`${w * 100}%`} height={18} />
            ))}
          </Box>
        ) : keys.isError ? (
          <Hint tone="error">{t("browser.loadFailed", { message: toAppError(keys.error).message })}</Hint>
        ) : entries.length === 0 ? (
          <Hint>
            {otherEnvs.length > 0 ? (
              <>
                {t("explorer.emptyHere", { namespace: snap.namespace, environment: snap.environment })}
                <Box sx={{ display: "flex", flexWrap: "wrap", gap: sp[1], mt: sp[2] }}>
                  {otherEnvs.map((env) => (
                    <ButtonBase
                      key={env}
                      onClick={() => changeLocation(snap.namespace, env)}
                      sx={{
                        display: "inline-flex",
                        alignItems: "center",
                        gap: sp[1],
                        px: sp[2],
                        py: "2px",
                        borderRadius: "4px",
                        border: hairline,
                        bgcolor: ground.cloud,
                        fontFamily: font.mono,
                        fontSize: 12,
                        color: ink.body,
                        "&:hover": { borderColor: ground.lineStrong, bgcolor: ground.mist },
                      }}
                    >
                      <EnvBand env={env} />
                      {env}
                    </ButtonBase>
                  ))}
                </Box>
              </>
            ) : (
              t("browser.empty")
            )}
          </Hint>
        ) : visible.length === 0 ? (
          <Hint>{t("explorer.noMatch", { filter })}</Hint>
        ) : (
          <Box component="ul" sx={{ listStyle: "none", m: 0, p: 0, pt: sp[1] }}>
            {groups.map((group) => {
              const isCollapsed = collapsed.has(group.dir);
              return (
                <Box component="li" key={group.dir || "/"} sx={{ m: 0, p: 0 }}>
                  {group.dir && (
                    <ButtonBase
                      onClick={() =>
                        setCollapsed((prev) => {
                          const next = new Set(prev);
                          if (next.has(group.dir)) next.delete(group.dir);
                          else next.add(group.dir);
                          return next;
                        })
                      }
                      aria-expanded={!isCollapsed}
                      sx={{
                        width: "100%",
                        justifyContent: "flex-start",
                        gap: sp[1],
                        px: sp[2],
                        height: 26,
                        mt: sp[1],
                        color: ink.faint,
                        fontFamily: font.mono,
                        fontSize: 11.5,
                        "&:hover": { color: ink.body },
                      }}
                    >
                      <ChevronRight
                        size={13}
                        style={{
                          transform: isCollapsed ? "rotate(0deg)" : "rotate(90deg)",
                          transition: "transform 150ms ease-out",
                        }}
                      />
                      <span>{group.dir}</span>
                      <Box component="span" sx={{ ml: "auto", fontVariantNumeric: "tabular-nums" }}>
                        {group.entries.length}
                      </Box>
                    </ButtonBase>
                  )}
                  {!isCollapsed && (
                    <Box component="ul" sx={{ listStyle: "none", m: 0, p: 0 }}>
                      {group.entries.map(({ entry, name }) => (
                        <KeyRow
                          key={entry.key}
                          entry={entry}
                          name={name}
                          nested={group.dir !== ""}
                          selected={entry.key === currentKey}
                          dirty={entry.key === currentKey && snap.dirty}
                          onOpen={() => openKey(entry.key)}
                        />
                      ))}
                    </Box>
                  )}
                </Box>
              );
            })}
          </Box>
        )}
      </Box>

      <NewKeyDialog
        open={newOpen}
        onClose={() => setNewOpen(false)}
        existing={entries.map((e) => e.key)}
        onCreate={(key) => {
          setNewOpen(false);
          openKey(key);
        }}
      />

    </Box>
  );
}

function KeyRow({
  entry,
  name,
  nested,
  selected,
  dirty,
  onOpen,
}: {
  entry: ConfigEntry;
  name: string;
  nested: boolean;
  selected: boolean;
  dirty: boolean;
  onOpen: () => void;
}) {
  const { t } = useTranslation();
  const detail = t("explorer.rowDetail", {
    version: entry.version,
    by: entry.updatedBy || "—",
    when: fmtRelative(entry.updatedAt),
  });
  return (
    <Box component="li" sx={{ m: 0, p: 0 }}>
      <Tooltip title={detail} placement="right" enterDelay={700}>
        <ButtonBase
          onClick={onOpen}
          aria-current={selected ? "page" : undefined}
          sx={{
            width: "100%",
            height: 28,
            justifyContent: "flex-start",
            gap: sp[2],
            pl: nested ? "26px" : sp[3],
            pr: sp[3],
            position: "relative",
            color: selected ? ink.strong : ink.body,
            bgcolor: selected ? state.activeSoft : "transparent",
            transition: "background-color 120ms ease-out",
            "&:hover": { bgcolor: selected ? state.activeSoft : ground.mist },
            "&::before": {
              content: '""',
              position: "absolute",
              left: 0,
              top: 4,
              bottom: 4,
              width: 2,
              borderRadius: "0 2px 2px 0",
              bgcolor: state.active,
              opacity: selected ? 1 : 0,
              transition: "opacity 120ms ease-out",
            },
          }}
        >
          <Box
            component="span"
            sx={{
              flex: 1,
              minWidth: 0,
              overflow: "hidden",
              textOverflow: "ellipsis",
              whiteSpace: "nowrap",
              textAlign: "left",
              fontFamily: font.mono,
              fontSize: 12.5,
              fontWeight: selected ? 500 : 400,
            }}
          >
            {name}
          </Box>
          {dirty && (
            <Box
              component="span"
              aria-label={t("edit.unsaved")}
              sx={{ width: 6, height: 6, borderRadius: "50%", bgcolor: state.active, flexShrink: 0 }}
            />
          )}
          {entry.isSecret && <Lock size={11} color={ink.faint} aria-label={t("edit.secret")} />}
          <Box
            component="span"
            sx={{ fontSize: 10.5, color: ink.faint, letterSpacing: "0.04em", flexShrink: 0 }}
          >
            {formatLabel(entry.format).toLowerCase()}
          </Box>
        </ButtonBase>
      </Tooltip>
    </Box>
  );
}

function FieldLabel({ htmlFor, children }: { htmlFor: string; children: React.ReactNode }) {
  return (
    <Typography
      component="label"
      htmlFor={htmlFor}
      sx={{ fontSize: 12, color: ink.muted, whiteSpace: "nowrap", pr: sp[1] }}
    >
      {children}
    </Typography>
  );
}

/** 环境色带:2×14 的圆角条,环境色只在这里出现。 */
export function EnvBand({ env, height = 14 }: { env: string; height?: number }) {
  return (
    <Box
      component="span"
      aria-hidden
      sx={{
        display: "inline-block",
        width: 3,
        height,
        borderRadius: 2,
        bgcolor: envTone(env).band,
        flexShrink: 0,
      }}
    />
  );
}

function Hint({ children, tone = "muted" }: { children: React.ReactNode; tone?: "muted" | "error" }) {
  return (
    <Typography
      component="div"
      sx={{
        px: sp[3],
        py: sp[4],
        fontSize: 12.5,
        lineHeight: 1.5,
        color: tone === "error" ? state.danger : ink.muted,
      }}
    >
      {children}
    </Typography>
  );
}
