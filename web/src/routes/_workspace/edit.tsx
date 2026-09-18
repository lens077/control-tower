import { createFileRoute, useBlocker, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { z } from "zod";
import Editor from "@monaco-editor/react";
import {
  Alert,
  Box,
  Button,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  FormControlLabel,
  IconButton,
  MenuItem,
  Skeleton,
  Switch,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import { ConnectError, Code } from "@connectrpc/connect";
import { toAppError } from "@/api/transport";
import { useTranslation } from "@/i18n";
import {
  CheckCircle2,
  History,
  Lock,
  Maximize2,
  Minimize2,
  Save,
  Trash2,
  Wand2,
  XCircle,
} from "lucide-react";
import { configApi, ConfigFormat } from "@/api";
import { FORMAT_OPTIONS, formatLabel, formatToLanguage } from "@/lib/format";
import {
  canFormat,
  FormatError,
  formatContent,
  type FormatIssue,
  validateContent,
  VALID,
} from "@/lib/validate";
import { fmtAbsolute, fmtRelative } from "@/lib/time";
import { editorStore, setDirty, setEnvironment, setNamespace } from "@/store/editor";
import { CLOUD_THEME, defineCloudTheme } from "@/monaco-theme";
import { keyframes } from "@emotion/react";
import { envTone, font, fullscreenOverlay, grain, ground, hairline, ink, sp, state } from "@/styles/tokens";
import { EnvBand } from "@/components/Explorer";

const SearchSchema = z.object({
  ns: z.string().default("ecommerce"),
  env: z.string().default("dev"),
  key: z.string(),
});

export const Route = createFileRoute("/_workspace/edit")({
  component: EditPage,
  validateSearch: SearchSchema,
});

/** Monaco marker 的 owner。格式校验和服务端报错各占一个,互不覆盖。 */
const MARKER_FORMAT = "config-format";
const MARKER_SERVER = "server";

/** 边打字边解析的防抖窗口 */
const VALIDATE_DEBOUNCE_MS = 300;

/** 后端对密钥值的脱敏占位,与 history.tsx 保持一致 */
const MASKED = "******";

const PROPS_WIDTH = 248;

/** 保存成功:紫点变绿、放大一下,再随文字一起淡出 —— 整个页面唯一一段编排过的动效。 */
const savedPulse = keyframes`
  0%   { opacity: 0; transform: translateX(-4px); }
  12%  { opacity: 1; transform: translateX(0); }
  80%  { opacity: 1; }
  100% { opacity: 0; }
`;
const savedDot = keyframes`
  0%   { background-color: ${state.active}; transform: scale(1); }
  25%  { background-color: ${state.success}; transform: scale(1.6); }
  45%  { transform: scale(1); }
  100% { background-color: ${state.success}; transform: scale(1); }
`;

/** 表单的「已保存」基线,用来算脏标记。 */
interface Baseline {
  value: string;
  format: ConfigFormat;
  isSecret: boolean;
  description: string;
}

function EditPage() {
  const { t } = useTranslation();
  const { ns, env, key } = Route.useSearch();
  const navigate = useNavigate();
  const qc = useQueryClient();

  const [value, setValue] = useState("");
  const [format, setFormat] = useState<ConfigFormat>(ConfigFormat.YAML);
  const [isSecret, setIsSecret] = useState(false);
  const [description, setDescription] = useState("");
  const [comment, setComment] = useState("");
  const [isNew, setIsNew] = useState(false);
  const [baseline, setBaseline] = useState<Baseline>({
    value: "",
    format: ConfigFormat.YAML,
    isSecret: false,
    description: "",
  });
  const [saveError, setSaveError] = useState<string | null>(null);
  const [savedAt, setSavedAt] = useState<number | null>(null);
  const [check, setCheck] = useState(VALID);
  const [isFullscreen, setIsFullscreen] = useState(false);

  const editorRef = useRef<any>(null);
  const monacoRef = useRef<any>(null);

  // 深链打开时把资源栏对齐到这个 key 所在的位置
  useEffect(() => {
    setNamespace(ns);
    setEnvironment(env);
  }, [ns, env]);

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ["getKey", ns, env, key],
    queryFn: async ({ signal }) => {
      try {
        return await configApi.getKey(ns, env, key, signal);
      } catch (e) {
        // 新建 key:后端返回 NotFound,视为空白新条目
        if (ConnectError.from(e).code === Code.NotFound) return null;
        throw e;
      }
    },
    retry: false,
  });

  // 首次加载后回填表单
  useEffect(() => {
    if (data === undefined) return;
    if (data === null || !data.entry) {
      setIsNew(true);
      setBaseline({ value: "", format: ConfigFormat.YAML, isSecret: false, description: "" });
      return;
    }
    const e = data.entry;
    const fmt = e.format === ConfigFormat.UNSPECIFIED ? ConfigFormat.YAML : e.format;
    setIsNew(false);
    setValue(e.value);
    setFormat(fmt);
    setIsSecret(e.isSecret);
    setDescription(e.description);
    setBaseline({ value: e.value, format: fmt, isSecret: e.isSecret, description: e.description });
  }, [data]);

  // ------------------------------------------------------------ 脏标记

  const dirty =
    value !== baseline.value ||
    format !== baseline.format ||
    isSecret !== baseline.isSecret ||
    description !== baseline.description;

  useEffect(() => {
    setDirty(dirty);
    return () => setDirty(false);
  }, [dirty]);

  // 有未保存改动时拦一下站内跳转;刷新/关闭标签页交给浏览器的 beforeunload
  const blocker = useBlocker({
    shouldBlockFn: () => editorStore.dirty,
    enableBeforeUnload: () => editorStore.dirty,
    withResolver: true,
  });

  // ------------------------------------------------------------ 标注

  const applyMarkers = useCallback((owner: string, issues: FormatIssue[]) => {
    const m = monacoRef.current;
    const model = editorRef.current?.getModel();
    if (!m || !model) return;
    m.editor.setModelMarkers(
      model,
      owner,
      issues.map((i) => ({
        startLineNumber: i.line,
        startColumn: i.column,
        endLineNumber: i.endLine,
        endColumn: i.endColumn,
        message: i.message,
        severity: m.MarkerSeverity.Error,
      })),
    );
  }, []);

  // ------------------------------------------------------------ 实时校验

  // 边打字边按「下拉里选的格式」解析。防抖是为了不在每个按键上都跑一遍解析器。
  useEffect(() => {
    const timer = setTimeout(() => {
      const result = validateContent(value, format);
      setCheck(result);
      applyMarkers(MARKER_FORMAT, result.issues);
    }, VALIDATE_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [value, format, applyMarkers]);

  // ------------------------------------------------------------ 格式化

  const doFormat = useCallback(() => {
    if (!canFormat(format)) return;
    try {
      const formatted = formatContent(value, format);
      if (formatted !== value) setValue(formatted);
      setCheck(VALID);
      applyMarkers(MARKER_FORMAT, []);
      applyMarkers(MARKER_SERVER, []);
    } catch (e) {
      // 格式化失败只可能是因为解析不过,复用同一套错误展示
      if (e instanceof FormatError) {
        setCheck({ ok: false, issues: e.issues });
        applyMarkers(MARKER_FORMAT, e.issues);
      }
    }
  }, [value, format, applyMarkers]);

  // Monaco 的快捷键回调只在 onMount 时注册一次,拿不到后续的 state,所以过 ref
  const doFormatRef = useRef(doFormat);
  doFormatRef.current = doFormat;

  /** 点错误 chip 跳到出错的位置 */
  const revealIssue = (issue: FormatIssue) => {
    const ed = editorRef.current;
    if (!ed) return;
    ed.revealLineInCenter(issue.line);
    ed.setPosition({ lineNumber: issue.line, column: issue.column });
    ed.focus();
  };

  // ------------------------------------------------------------ 保存/删除

  const save = useMutation({
    mutationFn: () =>
      configApi.putKey({
        namespace: ns,
        environment: env,
        key,
        format,
        value,
        comment,
        isSecret,
        description,
      }),
    onSuccess: (res) => {
      setSaveError(null);
      setComment("");
      setSavedAt(Date.now());
      // 立刻把基线推到刚提交的内容,不等 getKey 重新拉回来
      setBaseline({ value, format, isSecret, description });
      applyMarkers(MARKER_SERVER, []);
      qc.invalidateQueries({ queryKey: ["getKey", ns, env, key] });
      qc.invalidateQueries({ queryKey: ["listKeys"] });
      // 新建 key 可能引入新的 namespace/environment,刷新下拉数据源
      qc.invalidateQueries({ queryKey: ["listNamespaces"] });
      if (res.entry) setIsNew(false);
    },
    onError: (e) => {
      const err = toAppError(e);
      setSaveError(err.message);
      // 将服务端语法校验错误标注到编辑器首行
      if (err.code === Code.InvalidArgument) {
        applyMarkers(MARKER_SERVER, [
          { line: 1, column: 1, endLine: 1, endColumn: 200, message: err.message },
        ]);
      }
    },
  });

  const saveRef = useRef<() => void>(() => {});
  const saveBlocked = !check.ok;
  saveRef.current = () => {
    if (!save.isPending && !saveBlocked) save.mutate();
  };

  const del = useMutation({
    mutationFn: () => configApi.deleteKey(ns, env, key),
    onSuccess: () => {
      // 删除后没有东西可保存,别让离开拦截把用户困在这里
      setDirty(false);
      qc.invalidateQueries({ queryKey: ["listKeys"] });
      // 删掉最后一个 key 时 namespace/environment 也会随之消失
      qc.invalidateQueries({ queryKey: ["listNamespaces"] });
      navigate({ to: "/" });
    },
  });

  // ------------------------------------------------------------ 全屏

  // Monaco 聚焦时会先吃掉 Esc,所以除了这里的 window 监听,
  // onMount 里还注册了一条编辑器内的 Esc 命令。
  useEffect(() => {
    if (!isFullscreen) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setIsFullscreen(false);
    };
    window.addEventListener("keydown", onKeyDown, true);
    return () => window.removeEventListener("keydown", onKeyDown, true);
  }, [isFullscreen]);

  // 「刚刚保存」的绿色提示几秒后自己退场
  useEffect(() => {
    if (savedAt === null) return;
    const timer = setTimeout(() => setSavedAt(null), 4000);
    return () => clearTimeout(timer);
  }, [savedAt]);

  // ------------------------------------------------------------ 渲染

  const formattable = canFormat(format);
  const firstIssue = check.issues[0];
  const saveDisabledReason = saveBlocked
    ? t("edit.saveBlocked", { format: formatLabel(format) })
    : "";

  const editorNode = useMemo(
    () => (
      <Editor
        height="100%"
        language={formatToLanguage(format)}
        theme={CLOUD_THEME}
        value={value}
        onChange={(v) => setValue(v ?? "")}
        beforeMount={(monaco) => defineCloudTheme(monaco)}
        onMount={(editor, monaco) => {
          editorRef.current = editor;
          monacoRef.current = monaco;
          editor.addAction({
            id: "config-format-document",
            // Monaco 的 action 只在 onMount 注册一次,这条命令面板文案不跟随语言切换;
            // 它只出现在右键菜单/命令面板里,重进页面就是新语言,不值得为它加一层重注册
            label: t("edit.formatAction"),
            keybindings: [monaco.KeyMod.Alt | monaco.KeyMod.Shift | monaco.KeyCode.KeyF],
            run: () => doFormatRef.current(),
          });
          editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => saveRef.current());
          editor.addCommand(monaco.KeyCode.Escape, () => setIsFullscreen(false));
        }}
        options={{
          fontFamily: font.mono,
          fontSize: 13,
          lineHeight: 20,
          fontLigatures: false,
          minimap: { enabled: false },
          scrollBeyondLastLine: false,
          // 窄屏上把长 URL/target 折行,避免代码区在首屏右侧被截断。
          wordWrap: "on",
          tabSize: 2,
          automaticLayout: true,
          padding: { top: 12, bottom: 12 },
          renderLineHighlight: "line",
          lineNumbersMinChars: 3,
          folding: true,
          smoothScrolling: true,
          cursorBlinking: "smooth",
          overviewRulerBorder: false,
          hideCursorInOverviewRuler: true,
          scrollbar: { verticalScrollbarSize: 10, horizontalScrollbarSize: 10 },
        }}
      />
    ),
    [format, value],
  );

  if (isLoading) {
    return (
      <Box sx={{ p: sp[6], display: "grid", gap: sp[3] }}>
        <Skeleton variant="text" width={320} height={28} />
        <Skeleton variant="rounded" height={320} />
      </Box>
    );
  }
  if (isError) {
    return (
      <Box sx={{ p: sp[6] }}>
        <Alert severity="error">{t("edit.loadFailed", { message: toAppError(error).message })}</Alert>
      </Box>
    );
  }

  const entry = data?.entry;
  const tone = envTone(env);

  const statusChip = check.ok ? (
    <Chip
      size="small"
      color="success"
      icon={<CheckCircle2 size={13} />}
      label={t("edit.formatOk", { format: formatLabel(format) })}
    />
  ) : (
    <Tooltip title={t("edit.issueTooltip", { message: firstIssue.message })}>
      <Chip
        size="small"
        color="error"
        icon={<XCircle size={13} />}
        onClick={() => revealIssue(firstIssue)}
        label={t("validate.position", {
          line: firstIssue.line,
          column: firstIssue.column,
          message: firstIssue.message,
        })}
        sx={{ maxWidth: 480, cursor: "pointer" }}
      />
    </Tooltip>
  );

  return (
    <Box
      sx={{
        flex: 1,
        minHeight: 0,
        display: "flex",
        flexDirection: "column",
        bgcolor: ground.cloud,
        // 窄屏时编辑器与属性栏上下堆叠,整页自己滚;宽屏交给编辑器内部滚
        overflow: { xs: "auto", lg: "hidden" },
        ...(isFullscreen ? fullscreenOverlay : {}),
      }}
    >
      {/* 路径条:位置 + 状态 + key 级操作 */}
      <Box
        sx={{
          flexShrink: 0,
          minHeight: 44,
          display: "flex",
          alignItems: "center",
          flexWrap: "wrap",
          gap: sp[2],
          px: sp[4],
          py: sp[1],
          borderBottom: hairline,
        }}
      >
        <Box sx={{ display: "flex", alignItems: "center", gap: sp[1], minWidth: 0, fontFamily: font.mono, fontSize: 13 }}>
          <Box component="span" sx={{ color: ink.muted }}>
            {ns}
          </Box>
          <Box component="span" sx={{ color: ink.faint }}>
            ›
          </Box>
          <EnvBand env={env} height={12} />
          <Box component="span" sx={{ color: tone.text }}>
            {env}
          </Box>
          <Box component="span" sx={{ color: ink.faint }}>
            ›
          </Box>
          <Box
            component="span"
            sx={{ color: ink.strong, fontWeight: 500, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
          >
            {key}
          </Box>
        </Box>
        {isNew ? (
          <Chip label={t("edit.new")} size="small" color="info" />
        ) : (
          <Chip label={`v${entry?.version ?? "?"}`} size="small" />
        )}
        {isSecret && <Chip label={t("edit.secret")} size="small" color="warning" icon={<Lock size={12} />} />}
        {dirty ? (
          <Box sx={{ display: "flex", alignItems: "center", gap: sp[1], fontSize: 12, color: state.active }}>
            <Box component="span" sx={{ width: 7, height: 7, borderRadius: "50%", bgcolor: state.active }} />
            {t("edit.unsaved")}
          </Box>
        ) : savedAt !== null ? (
          <Box
            key={savedAt}
            role="status"
            sx={{
              display: "flex",
              alignItems: "center",
              gap: sp[1],
              fontSize: 12,
              color: state.success,
              animation: `${savedPulse} 4s cubic-bezier(0.16, 1, 0.3, 1) both`,
              "@media (prefers-reduced-motion: reduce)": { animation: "none" },
            }}
          >
            <Box
              component="span"
              sx={{
                width: 7,
                height: 7,
                borderRadius: "50%",
                bgcolor: state.success,
                animation: `${savedDot} 900ms cubic-bezier(0.16, 1, 0.3, 1) both`,
                "@media (prefers-reduced-motion: reduce)": { animation: "none" },
              }}
            />
            {t("edit.savedPushed")}
          </Box>
        ) : null}
        <Box sx={{ flex: 1 }} />
        <Box sx={{ display: "flex", alignItems: "center", gap: sp[1] }}>
          <Button
            startIcon={<History size={15} />}
            disabled={isNew}
            onClick={() => navigate({ to: "/history", search: { ns, env, key } })}
          >
            {t("edit.history")}
          </Button>
          <Button
            color="error"
            startIcon={<Trash2 size={15} />}
            disabled={isNew || del.isPending}
            onClick={() => del.mutate()}
          >
            {t("action.delete")}
          </Button>
          <Tooltip title={saveDisabledReason}>
            <span>
              <Button
                variant="contained"
                startIcon={<Save size={15} />}
                disabled={save.isPending || saveBlocked}
                onClick={() => save.mutate()}
                sx={{ minWidth: 88 }}
              >
                {save.isPending ? t("edit.saving") : t("action.save")}
              </Button>
            </span>
          </Tooltip>
        </Box>
      </Box>

      {(saveError || (isSecret && !isNew && entry?.value === MASKED)) && (
        <Box sx={{ flexShrink: 0, px: sp[4], pt: sp[3], display: "grid", gap: sp[2] }}>
          {saveError && (
            <Alert severity="error" onClose={() => setSaveError(null)}>
              {t("edit.saveFailed", { message: saveError })}
            </Alert>
          )}
          {isSecret && !isNew && entry?.value === MASKED && (
            <Alert severity="warning">{t("edit.secretWarning", { masked: MASKED })}</Alert>
          )}
        </Box>
      )}

      {/* 主体:编辑器占满,属性栏在右 */}
      <Box
        sx={{
          flex: 1,
          minHeight: 0,
          display: "flex",
          flexDirection: { xs: "column", lg: "row" },
        }}
      >
        <Box sx={{ flex: 1, minWidth: 0, minHeight: { xs: 240, lg: 0 }, display: "flex", flexDirection: "column" }}>
          {/* 工具轨:只放「对着编辑器本身」的操作 */}
          <Box
            sx={{
              flexShrink: 0,
              height: 34,
              display: "flex",
              alignItems: "center",
              gap: sp[2],
              px: sp[3],
              borderBottom: hairline,
              bgcolor: ground.mist,
            }}
          >
            {statusChip}
            <Box sx={{ flex: 1 }} />
            <Tooltip
              title={
                formattable
                  ? t("edit.formatTooltip")
                  : t("edit.formatUnavailable", { format: formatLabel(format) })
              }
            >
              <span>
                <Button size="small" startIcon={<Wand2 size={14} />} disabled={!formattable} onClick={doFormat} sx={{ minHeight: 26 }}>
                  {t("edit.format")}
                </Button>
              </span>
            </Tooltip>
            <Tooltip title={isFullscreen ? t("edit.exitFullscreen") : t("edit.fullscreen")}>
              <IconButton aria-label={isFullscreen ? t("edit.exitFullscreen") : t("edit.fullscreen")} onClick={() => setIsFullscreen((v) => !v)}>
                {isFullscreen ? <Minimize2 size={15} /> : <Maximize2 size={15} />}
              </IconButton>
            </Tooltip>
          </Box>
          <Box sx={{ flex: 1, minHeight: 0 }}>{editorNode}</Box>
        </Box>

        {!isFullscreen && (
          <Box
            component="aside"
            aria-label={t("edit.properties")}
            sx={{
              width: { xs: "100%", lg: PROPS_WIDTH },
              flexShrink: 0,
              borderLeft: { lg: hairline },
              borderTop: { xs: hairline, lg: "none" },
              bgcolor: ground.mist,
              ...grain,
              p: sp[3],
              display: "grid",
              gridTemplateColumns: { xs: "1fr 1fr", sm: "repeat(4, 1fr)", lg: "1fr" },
              alignContent: "start",
              gap: sp[3],
              overflow: "auto",
            }}
          >
            <TextField
              select
              label={t("edit.formatLabel")}
              size="small"
              value={format}
              onChange={(e) => setFormat(Number(e.target.value) as ConfigFormat)}
            >
              {FORMAT_OPTIONS.map((f) => (
                <MenuItem key={f} value={f}>
                  {formatLabel(f)}
                </MenuItem>
              ))}
            </TextField>
            <FormControlLabel
              control={<Switch checked={isSecret} onChange={(e) => setIsSecret(e.target.checked)} />}
              label={
                <Box>
                  <Typography sx={{ fontSize: 13, color: ink.strong }}>{t("edit.secret")}</Typography>
                  <Typography sx={{ fontSize: 11.5, color: ink.faint, lineHeight: 1.3 }}>{t("edit.secretHint")}</Typography>
                </Box>
              }
              sx={{ alignItems: "flex-start", "& .MuiSwitch-root": { mt: "2px" } }}
            />
            <TextField
              label={t("edit.description")}
              size="small"
              multiline
              minRows={2}
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              sx={{ gridColumn: { xs: "1 / -1", lg: "auto" } }}
            />
            <TextField
              label={t("edit.comment")}
              size="small"
              multiline
              minRows={2}
              value={comment}
              onChange={(e) => setComment(e.target.value)}
              helperText={t("edit.commentHint")}
              sx={{ gridColumn: { xs: "1 / -1", lg: "auto" } }}
            />
            {entry && (
              <Box sx={{ gridColumn: "1 / -1", pt: sp[2], borderTop: hairline, display: "grid", gap: sp[1] }}>
                <Meta label={t("edit.meta.version")} value={`v${entry.version}`} mono />
                <Meta label={t("edit.meta.updatedBy")} value={entry.updatedBy || "—"} />
                <Meta
                  label={t("edit.meta.updatedAt")}
                  value={fmtRelative(entry.updatedAt)}
                  title={fmtAbsolute(entry.updatedAt)}
                />
                <Meta label={t("edit.meta.createdAt")} value={fmtAbsolute(entry.createdAt)} />
              </Box>
            )}
          </Box>
        )}
      </Box>

      {/* 有未保存改动时的离开确认 */}
      <Dialog open={blocker.status === "blocked"} onClose={() => blocker.reset?.()}>
        <DialogTitle>{t("edit.leave.title")}</DialogTitle>
        <DialogContent>
          <DialogContentText>{t("edit.leave.body", { key })}</DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => blocker.reset?.()}>{t("edit.leave.stay")}</Button>
          <Button variant="contained" color="error" onClick={() => blocker.proceed?.()}>
            {t("edit.leave.discard")}
          </Button>
        </DialogActions>
      </Dialog>
    </Box>
  );
}

function Meta({ label, value, mono, title }: { label: string; value: string; mono?: boolean; title?: string }) {
  return (
    <Box sx={{ display: "flex", justifyContent: "space-between", gap: sp[2], fontSize: 12 }} title={title}>
      <Box component="span" sx={{ color: ink.faint }}>
        {label}
      </Box>
      <Box component="span" sx={{ color: ink.body, fontFamily: mono ? font.mono : undefined, textAlign: "right" }}>
        {value}
      </Box>
    </Box>
  );
}
