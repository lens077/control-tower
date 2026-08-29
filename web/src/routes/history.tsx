import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState } from "react";
import { z } from "zod";
import { DiffEditor } from "@monaco-editor/react";
import {
  Alert,
  Box,
  Button,
  Card,
  Chip,
  CircularProgress,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  Divider,
  ToggleButton,
  ToggleButtonGroup,
  Tooltip,
  Typography,
} from "@mui/material";
import { ArrowLeft, RefreshCw, RotateCcw } from "lucide-react";
import { toAppError } from "@/api/transport";
import { formatDate, i18next, useTranslation } from "@/i18n";
import { configApi, ConfigFormat } from "@/api";
import { formatToLanguage } from "@/lib/format";
import { lineDelta } from "@/lib/linediff";
import { sp } from "@/styles/glass";

const SearchSchema = z.object({
  ns: z.string().default("ecommerce"),
  env: z.string().default("dev"),
  key: z.string(),
});

export const Route = createFileRoute("/history")({
  component: HistoryPage,
  validateSearch: SearchSchema,
});

/** 后端对密钥的历史值也做脱敏,占位值与 GetKey 一致 */
const MASKED = "******";

const LIST_WIDTH = 340;
const HAIRLINE = "1px solid rgba(15, 23, 42, 0.08)";

type Timestamp = { seconds: bigint; nanos: number };

function toDate(ts?: Timestamp): Date | null {
  if (!ts) return null;
  return new Date(Number(ts.seconds) * 1000 + Math.floor(ts.nanos / 1e6));
}

function fmtAbsolute(ts?: Timestamp): string {
  return formatDate(toDate(ts));
}

/**
 * 「3 分钟前」。列表里扫一眼就知道新旧,精确时间放 tooltip。
 *
 * 是模块级函数,拿不到组件里的 t —— 走 i18next.t 在调用时解析。
 * 调用点在 render 里,切语言时组件会重渲染,文案跟着变。
 */
function fmtRelative(ts?: Timestamp): string {
  const d = toDate(ts);
  if (!d) return "";
  const sec = Math.round((Date.now() - d.getTime()) / 1000);
  if (sec < 60) return i18next.t("config:history.relative.justNow");
  const min = Math.round(sec / 60);
  if (min < 60) return i18next.t("config:history.relative.minutes", { value: min });
  const hour = Math.round(min / 60);
  if (hour < 24) return i18next.t("config:history.relative.hours", { value: hour });
  const day = Math.round(hour / 24);
  if (day < 30) return i18next.t("config:history.relative.days", { value: day });
  return formatDate(d);
}

function HistoryPage() {
  const { t } = useTranslation();
  const { ns, env, key } = Route.useSearch();
  const navigate = useNavigate();
  const qc = useQueryClient();

  const [selected, setSelected] = useState<number | null>(null);
  const [compareWith, setCompareWith] = useState<"current" | "prev">("current");
  const [pendingRollback, setPendingRollback] = useState<number | null>(null);

  // 换了 key 就得丢掉上一个 key 的选中版本号,否则会指向一个不存在的版本
  useEffect(() => {
    setSelected(null);
  }, [ns, env, key]);

  const current = useQuery({
    queryKey: ["getKey", ns, env, key],
    queryFn: ({ signal }) => configApi.getKey(ns, env, key, signal),
    retry: false,
  });

  const revisions = useQuery({
    queryKey: ["listRevisions", ns, env, key],
    queryFn: ({ signal }) => configApi.listRevisions(ns, env, key, signal),
    retry: false,
  });

  const rollback = useMutation({
    mutationFn: (version: number) =>
      configApi.rollback(ns, env, key, version, t("history.rollbackComment", { version })),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["getKey", ns, env, key] });
      qc.invalidateQueries({ queryKey: ["listRevisions", ns, env, key] });
      qc.invalidateQueries({ queryKey: ["listKeys"] });
      navigate({ to: "/edit", search: { ns, env, key } });
    },
  });

  const entry = current.data?.entry;
  // 后端按 version 倒序返回:revs[0] 最新
  const revs = revisions.data?.revisions ?? [];
  const language = formatToLanguage(entry?.format ?? ConfigFormat.YAML);
  // 密钥的历史值同样是 ******,拿它做 diff 没有意义
  const masked = entry?.isSecret === true || revs.some((r) => r.value === MASKED);

  // 每一版相对上一版改了多少行。值都在手上,顺手算出来,列表才有信息量。
  const rows = useMemo(
    () =>
      revs.map((rev, i) => {
        const older = revs[i + 1];
        return {
          rev,
          // 初始版本没有可比对象;脱敏后比了也是零
          delta: older && !masked ? lineDelta(older.value, rev.value) : null,
          unchanged: older != null && !masked && older.value === rev.value,
          isOldest: i === revs.length - 1,
        };
      }),
    [revs, masked],
  );

  // 默认选中次新版本:历史页最常见的问题是「上一次改了什么」
  const defaultVersion = revs.length > 1 ? revs[1].version : revs[0]?.version;
  const activeVersion = selected ?? defaultVersion ?? null;
  const activeIdx = revs.findIndex((r) => r.version === activeVersion);
  const activeRev = activeIdx >= 0 ? revs[activeIdx] : undefined;
  const olderRev = activeIdx >= 0 ? revs[activeIdx + 1] : undefined;

  // 「对比当前」= 选中版 → 线上值;「对比上一版」= 更旧的一版 → 选中版(即这一版改了什么)
  const compareCurrent = compareWith === "current";
  const left = compareCurrent ? activeRev : olderRev;
  const right = compareCurrent ? undefined : activeRev;
  const rightValue = compareCurrent ? (entry?.value ?? "") : (right?.value ?? "");
  const rightLabel = compareCurrent
    ? t("history.currentVersion", { version: entry?.version ?? "?" })
    : `v${right?.version ?? "?"}`;

  const isCurrent = (version: number) => entry != null && version === entry.version;

  // ---------------------------------------------------------------- 版本列表

  let listBody: React.ReactNode;
  if (revisions.isLoading) {
    listBody = (
      <Box sx={{ p: sp[6], textAlign: "center" }}>
        <CircularProgress size={22} />
      </Box>
    );
  } else if (revisions.isError) {
    // 原先这里把加载失败也画成「暂无历史」—— 一个 v22 的 key 看着像从没改过,
    // 错的地方(后端/网关)完全被掩盖掉了
    listBody = (
      <Box sx={{ p: sp[3] }}>
        <Alert
          severity="error"
          action={
            <Button size="small" color="inherit" onClick={() => revisions.refetch()}>
              {t("action.retry")}
            </Button>
          }
        >
          {t("history.loadFailed", { message: toAppError(revisions.error).message })}
        </Alert>
      </Box>
    );
  } else if (rows.length === 0) {
    listBody = (
      <Box sx={{ p: sp[4] }}>
        <Typography color="text.secondary" variant="body2">
          {t("history.noRevisions")}
        </Typography>
      </Box>
    );
  } else {
    listBody = (
      <Box component="ul" sx={{ listStyle: "none", m: 0, p: 0 }}>
        {rows.map(({ rev, delta, unchanged, isOldest }) => {
          const active = rev.version === activeVersion;
          return (
            <Box
              component="li"
              key={rev.version}
              onClick={() => setSelected(rev.version)}
              sx={{
                px: sp[3],
                py: sp[2],
                cursor: "pointer",
                borderBottom: HAIRLINE,
                borderLeft: "3px solid",
                borderLeftColor: active ? "primary.main" : "transparent",
                background: active ? "rgba(37, 99, 235, 0.08)" : "transparent",
                "&:hover": {
                  background: active ? "rgba(37, 99, 235, 0.12)" : "rgba(15, 23, 42, 0.04)",
                },
              }}
            >
              <Box sx={{ display: "flex", alignItems: "center", gap: sp[2] }}>
                <Typography sx={{ fontFamily: "monospace", fontWeight: 700, fontSize: 14 }}>
                  v{rev.version}
                </Typography>
                {isCurrent(rev.version) && (
                  <Chip label={t("history.current")} size="small" color="primary" />
                )}
                {isOldest && <Chip label={t("history.initial")} size="small" variant="outlined" />}
                <Box sx={{ flex: 1 }} />
                {unchanged ? (
                  <Tooltip title={t("history.sameAsPrev")}>
                    <Chip label={t("history.unchanged")} size="small" variant="outlined" />
                  </Tooltip>
                ) : (
                  delta && (
                    <Typography
                      component="span"
                      sx={{ fontFamily: "monospace", fontSize: 12, whiteSpace: "nowrap" }}
                    >
                      <Box component="span" sx={{ color: "success.main" }}>
                        +{delta.added}
                      </Box>{" "}
                      <Box component="span" sx={{ color: "error.main" }}>
                        −{delta.removed}
                      </Box>
                    </Typography>
                  )
                )}
              </Box>

              <Typography
                variant="body2"
                sx={{
                  mt: sp[1],
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                  color: rev.comment ? "text.primary" : "text.disabled",
                  fontStyle: rev.comment ? "normal" : "italic",
                }}
                title={rev.comment}
              >
                {rev.comment || t("history.noComment")}
              </Typography>

              <Tooltip title={fmtAbsolute(rev.createdAt)} placement="right">
                <Typography variant="caption" color="text.secondary" sx={{ display: "block" }}>
                  {rev.author || "—"} · {fmtRelative(rev.createdAt)}
                </Typography>
              </Tooltip>
            </Box>
          );
        })}
      </Box>
    );
  }

  // ---------------------------------------------------------------- diff 面板

  let diffBody: React.ReactNode;
  if (masked && revs.length > 0) {
    diffBody = (
      <Alert severity="info" sx={{ m: sp[3] }}>
        {t("history.maskedNotice", { masked: MASKED })}
      </Alert>
    );
  } else if (!activeRev) {
    diffBody = (
      <Box sx={{ p: sp[6], textAlign: "center" }}>
        <Typography color="text.secondary" variant="body2">
          {revisions.isLoading ? t("history.loading") : t("history.pickVersion")}
        </Typography>
      </Box>
    );
  } else if (!compareCurrent && !olderRev) {
    diffBody = (
      <Alert severity="info" sx={{ m: sp[3] }}>
        {t("history.oldestNotice", { version: activeRev.version })}
      </Alert>
    );
  } else {
    diffBody = (
      <Box sx={{ flex: 1, minHeight: 0 }}>
        <DiffEditor
          height="100%"
          language={language}
          original={left?.value ?? ""}
          modified={rightValue}
          options={{
            fontSize: 13,
            minimap: { enabled: false },
            readOnly: true,
            scrollBeyondLastLine: false,
            automaticLayout: true,
            // 左右分栏在窄容器里会把每一栏挤到放不下一行配置(行尾直接被截掉),
            // 低于这个宽度就自动切成上下对照的内联视图 —— 宁可少一栏,也要看得全
            renderSideBySide: true,
            useInlineViewWhenSpaceIsLimited: true,
            renderSideBySideInlineBreakpoint: 900,
            // 长值(连接串、URL)照样会超出一行,让它折行而不是被裁掉
            wordWrap: "on",
            diffWordWrap: "inherit",
          }}
        />
      </Box>
    );
  }

  // ---------------------------------------------------------------- 渲染

  return (
    <Box
      sx={{
        width: "100%",
        flex: 1,
        minHeight: 0,
        display: "flex",
        flexDirection: "column",
        gap: sp[3],
      }}
    >
      {/* 头部:直接铺在页面上,不再套一层卡片 */}
      <Box
        sx={{ display: "flex", alignItems: "center", flexWrap: "wrap", gap: sp[2], flexShrink: 0 }}
      >
        <Button
          startIcon={<ArrowLeft size={18} />}
          onClick={() => navigate({ to: "/edit", search: { ns, env, key } })}
        >
          {t("history.back")}
        </Button>
        <Typography sx={{ fontFamily: "monospace", fontWeight: 700 }}>{key}</Typography>
        <Chip label={`${ns}/${env}`} size="small" variant="outlined" />
        {entry && (
          <Chip label={t("history.currentVersion", { version: entry.version })} size="small" />
        )}
        {entry?.isSecret && (
          <Chip label={t("history.secret")} size="small" color="warning" variant="outlined" />
        )}
        <Box sx={{ flex: 1 }} />
        <Button
          size="small"
          startIcon={<RefreshCw size={16} />}
          disabled={revisions.isFetching}
          onClick={() => {
            revisions.refetch();
            current.refetch();
          }}
        >
          {t("action.refresh")}
        </Button>
      </Box>

      {current.isError && (
        <Alert severity="error" sx={{ flexShrink: 0 }}>
          {t("history.readCurrentFailed", { message: toAppError(current.error).message })}
        </Alert>
      )}
      {rollback.isError && (
        <Alert severity="error" sx={{ flexShrink: 0 }} onClose={() => rollback.reset()}>
          {t("history.rollbackFailed", { message: toAppError(rollback.error).message })}
        </Alert>
      )}

      {/* 主体:一块面板内左右分栏,不再是两张互相挤压的卡片 */}
      <Card
        sx={{
          flex: 1,
          minHeight: 0,
          display: "flex",
          flexDirection: { xs: "column", md: "row" },
          overflow: "hidden",
          p: 0,
        }}
      >
        {/* 左:版本列表 */}
        <Box
          sx={{
            width: { xs: "100%", md: LIST_WIDTH },
            flexShrink: 0,
            display: "flex",
            flexDirection: "column",
            minWidth: 0,
            minHeight: 0,
            maxHeight: { xs: 260, md: "none" },
            borderRight: { md: HAIRLINE },
            borderBottom: { xs: HAIRLINE, md: "none" },
          }}
        >
          <Box
            sx={{
              display: "flex",
              alignItems: "baseline",
              gap: sp[2],
              px: sp[3],
              py: sp[2],
              flexShrink: 0,
            }}
          >
            <Typography variant="subtitle2" sx={{ fontWeight: 700 }}>
              {t("history.title")}
            </Typography>
            {rows.length > 0 && (
              <Typography variant="caption" color="text.secondary">
                {t("history.count", { total: rows.length })}
              </Typography>
            )}
          </Box>
          <Divider />
          <Box sx={{ flex: 1, minHeight: 0, overflow: "auto" }}>{listBody}</Box>
        </Box>

        {/* 右:差异。minWidth:0 是关键 —— 否则 Monaco 的固有宽度会把这一栏顶成窄条 */}
        <Box sx={{ flex: 1, minWidth: 0, minHeight: 0, display: "flex", flexDirection: "column" }}>
          <Box
            sx={{
              display: "flex",
              alignItems: "center",
              flexWrap: "wrap",
              gap: sp[2],
              px: sp[3],
              py: sp[2],
              flexShrink: 0,
            }}
          >
            <ToggleButtonGroup
              size="small"
              exclusive
              value={compareWith}
              onChange={(_, v) => v && setCompareWith(v)}
            >
              <ToggleButton value="current">{t("history.compareCurrent")}</ToggleButton>
              <ToggleButton value="prev">{t("history.comparePrev")}</ToggleButton>
            </ToggleButtonGroup>

            {activeRev && (
              <Typography variant="body2" color="text.secondary" sx={{ fontFamily: "monospace" }}>
                {t("history.diffLabel", {
                  left: left ? `v${left.version}` : t("history.empty"),
                  right: rightLabel,
                })}
              </Typography>
            )}

            <Box sx={{ flex: 1 }} />

            {activeRev && (
              <Tooltip
                title={
                  isCurrent(activeRev.version)
                    ? t("history.alreadyCurrent")
                    : t("history.rollbackTooltip", { version: activeRev.version })
                }
              >
                <span>
                  <Button
                    size="small"
                    variant="contained"
                    startIcon={<RotateCcw size={16} />}
                    disabled={rollback.isPending || isCurrent(activeRev.version)}
                    onClick={() => setPendingRollback(activeRev.version)}
                  >
                    {t("history.rollbackTo", { version: activeRev.version })}
                  </Button>
                </span>
              </Tooltip>
            )}
          </Box>
          <Divider />
          {diffBody}
        </Box>
      </Card>

      {/* 回滚会产生一个新版本并立刻下发给在跑的服务,值得先问一句 */}
      <Dialog open={pendingRollback !== null} onClose={() => setPendingRollback(null)}>
        <DialogTitle>{t("history.rollbackDialogTitle", { version: pendingRollback })}</DialogTitle>
        <DialogContent>
          <DialogContentText>
            {t("history.rollbackDialogBody", {
              version: pendingRollback,
              next: (entry?.version ?? 0) + 1,
            })}
          </DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setPendingRollback(null)}>{t("action.cancel")}</Button>
          <Button
            variant="contained"
            disabled={rollback.isPending}
            onClick={() => {
              if (pendingRollback !== null) rollback.mutate(pendingRollback);
              setPendingRollback(null);
            }}
          >
            {t("history.confirmRollback")}
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  );
}
