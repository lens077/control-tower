import { useEffect, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  Autocomplete,
  Box,
  Button,
  Card,
  Collapse,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  IconButton,
  MenuItem,
  Skeleton,
  Stack,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import { ChevronDown, Copy, Eye, KeyRound, Plus, RefreshCw, ShieldX, X } from "lucide-react";
import { EnvBand } from "@/components/Explorer";
import { Meta, PageFrame } from "@/components/PageFrame";
import { fmtAbsolute, fmtRelative } from "@/lib/time";
import { useSnapshot } from "valtio";
import { configApi } from "@/api";
import { describeError, isSlug } from "@/api/validation";
import { MachineTokenRole, type MachineTokenMeta } from "@/gen/api";
import { useTranslation } from "@/i18n";
import { forgetIssuedToken, issuedTokenStore, rememberIssuedToken } from "@/store/issued-tokens";
import { font, ground, hairline, ink, sp, state } from "@/styles/tokens";

interface IssueForm {
  serviceName: string;
  environment: string;
  allowedNamespaces: string;
  note: string;
  role: MachineTokenRole;
}

const EMPTY_ISSUE_FORM: IssueForm = {
  serviceName: "",
  environment: "",
  allowedNamespaces: "",
  note: "",
  role: MachineTokenRole.SERVICE,
};

function parseNamespaces(value: string): string[] {
  return [...new Set(value.split(/[\n,]/).map((item) => item.trim()).filter(Boolean))];
}

interface TokensPageProps {
  api?: typeof configApi;
  initialIssueOpen?: boolean;
  initialIssueForm?: Partial<IssueForm>;
  initialFilters?: { serviceName?: string; environment?: string };
}

export function TokensPage({ api = configApi, initialIssueOpen = false, initialIssueForm, initialFilters }: TokensPageProps = {}) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [serviceName, setServiceName] = useState(initialFilters?.serviceName ?? "");
  const [environment, setEnvironment] = useState(initialFilters?.environment ?? "");
  const [filtersOpen, setFiltersOpen] = useState(Boolean(initialFilters?.serviceName || initialFilters?.environment));
  const [issueOpen, setIssueOpen] = useState(initialIssueOpen);
  const [issueForm, setIssueForm] = useState<IssueForm>({ ...EMPTY_ISSUE_FORM, ...initialIssueForm });
  // 弹窗里正在展示的明文。关闭弹窗只是收起视图，明文仍留在 issuedTokenStore 里可再次打开。
  const [issuedView, setIssuedView] = useState<{ id: string; token: string } | null>(null);
  const [confirmForget, setConfirmForget] = useState(false);
  const [copied, setCopied] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState<MachineTokenMeta | null>(null);
  const issuedPlaintexts = useSnapshot(issuedTokenStore).plaintexts;

  const tokensQuery = useQuery({
    queryKey: ["machineTokens", serviceName, environment],
    queryFn: ({ signal }) => api.listMachineTokens({ serviceName, environment }, signal),
  });

  const issueMutation = useMutation({
    mutationFn: () =>
      api.issueMachineToken({
        serviceName: issueForm.serviceName.trim(),
        environment: issueForm.environment.trim(),
        allowedNamespaces: parseNamespaces(issueForm.allowedNamespaces),
        note: issueForm.note.trim(),
        role: issueForm.role,
      }),
    onSuccess: async (response) => {
      const id = response.meta?.id ?? "";
      rememberIssuedToken(id, response.token);
      setIssuedView({ id, token: response.token });
      setCopied(false);
      setIssueOpen(false);
      setIssueForm(EMPTY_ISSUE_FORM);
      await queryClient.invalidateQueries({ queryKey: ["machineTokens"] });
    },
  });

  const revokeMutation = useMutation({
    mutationFn: (id: string) => api.revokeMachineToken(id),
    onSuccess: async (_response, id) => {
      // 已吊销的 token 明文再留着没有意义。
      forgetIssuedToken(id);
      setRevokeTarget(null);
      await queryClient.invalidateQueries({ queryKey: ["machineTokens"] });
    },
  });

  useEffect(() => () => setIssuedView(null), []);

  const tokens = tokensQuery.data?.tokens ?? [];
  const serviceOptions = [...new Set(tokens.map((token) => token.serviceName).filter(Boolean))].sort();
  const environmentOptions = [...new Set(tokens.map((token) => token.environment).filter(Boolean))].sort();
  // 服务名/环境在 proto 里是 `^[a-z][a-z0-9-]*$`（见 IssueMachineTokenRequest）。
  // 本地先拦一次：中文或大写在这里就红字提示，不用换一个来回去换一句英文正则。
  const serviceNameInvalid = issueForm.serviceName.trim() !== "" && !isSlug(issueForm.serviceName.trim());
  const environmentInvalid = issueForm.environment.trim() !== "" && !isSlug(issueForm.environment.trim());
  const issueValid =
    issueForm.serviceName.trim() !== "" &&
    issueForm.environment.trim() !== "" &&
    !serviceNameInvalid &&
    !environmentInvalid;

  // 收起弹窗：明文继续留在内存里，列表卡片上的「查看明文」可以再次打开。
  const hideIssuedToken = () => {
    setIssuedView(null);
    setCopied(false);
  };

  // 确认后丢弃：从内存里抹掉，之后真的取不回来了。
  const forgetIssuedView = () => {
    if (issuedView) forgetIssuedToken(issuedView.id);
    setConfirmForget(false);
    setIssuedView(null);
    setCopied(false);
  };

  const copyIssuedToken = async () => {
    if (!issuedView) return;
    await navigator.clipboard.writeText(issuedView.token);
    setCopied(true);
  };

  // 复制并关闭：剪贴板失败（无权限/非安全上下文）也要让确认框照常出来，
  // 否则用户会卡在一个既没复制上、又没有反馈的弹窗里。
  const copyAndClose = async () => {
    try {
      await copyIssuedToken();
    } catch {
      // 剪贴板不可用（非安全上下文、权限被拒）不该卡住关闭流程；明文还在框里可以手动选中。
    }
    setConfirmForget(true);
  };

  const submitIssue = (event: FormEvent) => {
    event.preventDefault();
    if (issueValid) issueMutation.mutate();
  };

  return (
    <PageFrame
      title={t("tokens.title")}
      subtitle={t("tokens.subtitle")}
      actions={
        <>
          <Button variant="outlined" startIcon={<RefreshCw size={14} />} onClick={() => tokensQuery.refetch()}>
            {t("tokens.refresh")}
          </Button>
          <Button variant="contained" startIcon={<Plus size={14} />} onClick={() => setIssueOpen(true)}>
            {t("tokens.issue")}
          </Button>
        </>
      }
    >
      {/* 筛选默认收起;展开后既可下拉选择,也可输入关键字匹配。 */}
      <Box sx={{ borderBottom: hairline, pb: sp[2] }}>
        <Button
          variant="text"
          onClick={() => setFiltersOpen((open) => !open)}
          endIcon={<ChevronDown size={15} style={{ transform: filtersOpen ? "rotate(180deg)" : undefined, transition: "transform 150ms ease-out" }} />}
          aria-expanded={filtersOpen}
          aria-controls="token-filters"
          sx={{ px: 0, color: ink.body }}
        >
          {t("tokens.filters")}
        </Button>
        <Typography component="span" sx={{ ml: sp[2], fontSize: 12.5, color: ink.faint }}>
          {t("tokens.filterHint")}
        </Typography>
        <Collapse in={filtersOpen}>
          <Box id="token-filters" sx={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: sp[2], pt: sp[2] }}>
            <Autocomplete
              freeSolo
              size="small"
              options={serviceOptions}
              value={serviceName}
              onInputChange={(_, value) => setServiceName(value)}
              onChange={(_, value) => setServiceName(value ?? "")}
              sx={{ width: { xs: "100%", sm: 220 } }}
              renderInput={(params) => <TextField {...params} label={t("tokens.service")} placeholder={t("tokens.filterPlaceholder")} />}
            />
            <Autocomplete
              freeSolo
              size="small"
              options={environmentOptions}
              value={environment}
              onInputChange={(_, value) => setEnvironment(value)}
              onChange={(_, value) => setEnvironment(value ?? "")}
              sx={{ width: { xs: "100%", sm: 160 } }}
              renderInput={(params) => <TextField {...params} label={t("tokens.environment")} placeholder={t("tokens.filterPlaceholder")} />}
            />
            <Box sx={{ flex: 1 }} />
            {tokensQuery.data && <Typography sx={{ fontSize: 12.5, color: ink.faint, fontVariantNumeric: "tabular-nums" }}>{t("tokens.count", { count: tokens.length })}</Typography>}
          </Box>
        </Collapse>
      </Box>

      {tokensQuery.isLoading ? (
        <Box sx={{ display: "grid", gap: sp[2] }}>
          {[0, 1].map((i) => (
            <Skeleton key={i} variant="rounded" height={96} />
          ))}
        </Box>
      ) : tokensQuery.isError ? (
        <Alert severity="error">{t("tokens.loadFailed", { message: describeError(tokensQuery.error, t) })}</Alert>
      ) : tokens.length === 0 ? (
        <Box sx={{ py: sp[10], textAlign: "center", border: `1px dashed ${ground.lineStrong}`, borderRadius: "8px" }}>
          <KeyRound size={22} color={ink.faint} />
          <Typography sx={{ mt: sp[2], fontSize: 13, color: ink.muted }}>
            {t(serviceName || environment ? "tokens.emptyFiltered" : "tokens.empty")}
          </Typography>
        </Box>
      ) : (
        <Box sx={{ display: "grid", gap: sp[2] }}>
          {tokens.map((token) => (
            <TokenCard
              key={token.id}
              token={token}
              plaintext={issuedPlaintexts[token.id]}
              onView={(plaintext) => {
                setIssuedView({ id: token.id, token: plaintext });
                setCopied(false);
              }}
              onRevoke={() => setRevokeTarget(token)}
            />
          ))}
        </Box>
      )}

      <Dialog open={issueOpen} onClose={() => !issueMutation.isPending && setIssueOpen(false)} fullWidth maxWidth="sm">
        <Box component="form" onSubmit={submitIssue}>
          <DialogTitle>{t("tokens.issueDialog.title")}</DialogTitle>
          <DialogContent>
            <Stack spacing={sp[3]} sx={{ pt: sp[1] }}>
              {issueMutation.isError && (
                <Alert severity="error">
                  {t("tokens.issueDialog.failed", { message: describeError(issueMutation.error, t) })}
                </Alert>
              )}
              <TextField
                required
                autoFocus
                label={t("tokens.service")}
                value={issueForm.serviceName}
                error={serviceNameInvalid}
                helperText={serviceNameInvalid ? t("tokens.issueDialog.slugInvalid") : t("tokens.issueDialog.slugHelp")}
                onChange={(event) => setIssueForm((form) => ({ ...form, serviceName: event.target.value }))}
              />
              <TextField
                required
                label={t("tokens.environment")}
                value={issueForm.environment}
                error={environmentInvalid}
                helperText={environmentInvalid ? t("tokens.issueDialog.slugInvalid") : undefined}
                onChange={(event) => setIssueForm((form) => ({ ...form, environment: event.target.value }))}
              />
              <TextField
                select
                label={t("tokens.role")}
                value={issueForm.role}
                onChange={(event) => setIssueForm((form) => ({ ...form, role: Number(event.target.value) as MachineTokenRole }))}
              >
                <MenuItem value={MachineTokenRole.SERVICE}>{t("tokens.roleService")}</MenuItem>
                <MenuItem value={MachineTokenRole.OPERATOR}>{t("tokens.roleOperator")}</MenuItem>
              </TextField>
              <TextField
                multiline
                minRows={2}
                label={t("tokens.namespaces")}
                helperText={t("tokens.issueDialog.namespacesHelp")}
                value={issueForm.allowedNamespaces}
                onChange={(event) => setIssueForm((form) => ({ ...form, allowedNamespaces: event.target.value }))}
              />
              <TextField
                multiline
                minRows={2}
                label={t("tokens.note")}
                value={issueForm.note}
                onChange={(event) => setIssueForm((form) => ({ ...form, note: event.target.value }))}
              />
            </Stack>
          </DialogContent>
          <DialogActions>
            <Button onClick={() => setIssueOpen(false)} disabled={issueMutation.isPending}>{t("tokens.cancel")}</Button>
            <Button type="submit" variant="contained" disabled={!issueValid || issueMutation.isPending}>
              {issueMutation.isPending ? t("tokens.issueDialog.issuing") : t("tokens.issueDialog.submit")}
            </Button>
          </DialogActions>
        </Box>
      </Dialog>

      <Dialog open={issuedView !== null} onClose={hideIssuedToken} fullWidth maxWidth="sm">
        <DialogTitle sx={{ display: "flex", alignItems: "center", gap: sp[2] }}>
          <Box sx={{ flex: 1, minWidth: 0 }}>{t("tokens.issued.title")}</Box>
          {/* 右上角 X 是非破坏性的：只收起视图，明文仍留在内存里。 */}
          <Tooltip title={t("tokens.issued.hide")}>
            <IconButton aria-label={t("tokens.issued.hide")} size="small" onClick={hideIssuedToken}>
              <X size={18} />
            </IconButton>
          </Tooltip>
        </DialogTitle>
        <DialogContent>
          <Typography variant="subtitle2" sx={{ fontWeight: 700 }}>{t("tokens.issued.label")}</Typography>
          <Typography variant="body2" color="text.secondary" sx={{ mt: sp[1], mb: sp[2] }}>
            {t("tokens.issued.warning")}
          </Typography>
          <Stack direction={{ xs: "column", sm: "row" }} spacing={sp[2]} sx={{ alignItems: { sm: "center" } }}>
            <TextField
              fullWidth
              size="small"
              value={issuedView?.token ?? ""}
              onFocus={(event) => event.target.select()}
              slotProps={{
                htmlInput: {
                  readOnly: true,
                  spellCheck: false,
                  "data-testid": "issued-token",
                  "aria-label": t("tokens.issued.label"),
                  style: { fontFamily: "monospace" },
                },
              }}
            />
            <Button
              variant="outlined"
              color="inherit"
              startIcon={<Copy size={16} />}
              onClick={copyIssuedToken}
              sx={{ flexShrink: 0 }}
            >
              {copied ? t("tokens.issued.copied") : t("tokens.issued.copyToClipboard")}
            </Button>
          </Stack>
        </DialogContent>
        <DialogActions>
          <Button variant="outlined" color="inherit" onClick={copyAndClose}>
            {t("tokens.issued.copyAndClose")}
          </Button>
          <Button color="inherit" onClick={() => setConfirmForget(true)}>{t("tokens.issued.close")}</Button>
        </DialogActions>
      </Dialog>

      <Dialog open={confirmForget} onClose={() => setConfirmForget(false)} fullWidth maxWidth="xs">
        <DialogTitle>{t("tokens.issued.confirm.title")}</DialogTitle>
        <DialogContent>
          <Typography>{t("tokens.issued.confirm.body")}</Typography>
          <Alert severity="warning" sx={{ mt: sp[3] }}>{t("tokens.issued.confirm.warning")}</Alert>
        </DialogContent>
        <DialogActions>
          <Button color="inherit" onClick={() => setConfirmForget(false)}>{t("tokens.cancel")}</Button>
          <Button variant="outlined" color="error" onClick={forgetIssuedView}>
            {t("tokens.issued.confirm.submit")}
          </Button>
        </DialogActions>
      </Dialog>

      <Dialog open={revokeTarget !== null} onClose={() => !revokeMutation.isPending && setRevokeTarget(null)} fullWidth maxWidth="sm">
        <DialogTitle>{t("tokens.revoke.title")}</DialogTitle>
        <DialogContent>
          {revokeMutation.isError && <Alert severity="error" sx={{ mb: sp[2] }}>{t("tokens.revoke.failed", { message: describeError(revokeMutation.error, t) })}</Alert>}
          <Typography>{t("tokens.revoke.body", { service: revokeTarget?.serviceName, environment: revokeTarget?.environment })}</Typography>
          <Alert severity="warning" sx={{ mt: sp[3] }}>{t("tokens.revoke.watchWarning")}</Alert>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setRevokeTarget(null)} disabled={revokeMutation.isPending}>{t("tokens.cancel")}</Button>
          <Button
            color="error"
            variant="contained"
            disabled={revokeMutation.isPending || !revokeTarget}
            onClick={() => revokeTarget && revokeMutation.mutate(revokeTarget.id)}
          >
            {revokeMutation.isPending ? t("tokens.revoke.revoking") : t("tokens.revoke.submit")}
          </Button>
        </DialogActions>
      </Dialog>
    </PageFrame>
  );
}

function TokenCard({
  token,
  plaintext,
  onView,
  onRevoke,
}: {
  token: MachineTokenMeta;
  plaintext?: string;
  onView: (plaintext: string) => void;
  onRevoke: () => void;
}) {
  const { t } = useTranslation();
  const isOperator = token.role === MachineTokenRole.OPERATOR;
  return (
    <Card sx={{ opacity: token.disabled ? 0.72 : 1 }}>
      <Box sx={{ px: sp[4], py: sp[3], display: "flex", flexWrap: "wrap", alignItems: "center", gap: sp[2] }}>
        <KeyRound size={15} color={token.disabled ? ink.faint : state.active} />
        <Typography sx={{ fontFamily: font.mono, fontSize: 13.5, fontWeight: 500, color: ink.strong }}>
          {token.serviceName}
        </Typography>
        <Box sx={{ display: "inline-flex", alignItems: "center", gap: sp[1], fontFamily: font.mono, fontSize: 12.5, color: ink.muted }}>
          <EnvBand env={token.environment} height={12} />
          {token.environment}
        </Box>
        <Chip size="small" variant="outlined" label={t(isOperator ? "tokens.roleOperator" : "tokens.roleService")} />
        <Chip
          size="small"
          color={token.disabled ? undefined : "success"}
          variant={token.disabled ? "outlined" : "filled"}
          label={t(token.disabled ? "tokens.status.revoked" : "tokens.status.active")}
        />
        <Box sx={{ flex: 1 }} />
        {plaintext !== undefined && (
          <Button startIcon={<Eye size={14} />} onClick={() => onView(plaintext)}>
            {t("tokens.issued.view")}
          </Button>
        )}
        <Button color="error" startIcon={<ShieldX size={14} />} disabled={token.disabled} onClick={onRevoke}>
          {t("tokens.revoke.action")}
        </Button>
      </Box>
      <Box
        sx={{
          px: sp[4],
          py: sp[3],
          borderTop: hairline,
          display: "grid",
          gridTemplateColumns: { xs: "1fr 1fr", md: "repeat(3, minmax(0, 1fr)) 2fr" },
          gap: sp[3],
        }}
      >
        <Meta label={t("tokens.createdAt")} value={fmtAbsolute(token.createdAt)} />
        <Meta label={t("tokens.lastUsedAt")} value={<Tooltip title={fmtAbsolute(token.lastUsedAt, "")}><span>{fmtRelative(token.lastUsedAt)}</span></Tooltip>} />
        <Meta label={t("tokens.revokedAt")} value={fmtAbsolute(token.revokedAt)} />
        <Box sx={{ gridColumn: { xs: "1 / -1", md: "auto" } }}>
          <Typography sx={{ fontSize: 11.5, color: ink.faint, lineHeight: 1.3 }}>{t("tokens.namespaces")}</Typography>
          <Box sx={{ display: "flex", flexWrap: "wrap", gap: sp[1], mt: sp[1] }}>
            {token.allowedNamespaces.length === 0 && (
              <Typography sx={{ fontSize: 13, color: ink.faint }}>{t("tokens.ownNamespaceOnly")}</Typography>
            )}
            {token.allowedNamespaces.map((namespace) => (
              <Box
                key={namespace}
                component="span"
                sx={{
                  display: "inline-flex",
                  alignItems: "center",
                  px: sp[2],
                  height: 22,
                  border: hairline,
                  borderRadius: "4px",
                  fontFamily: font.mono,
                  fontSize: 12,
                  color: ink.body,
                }}
              >
                {namespace}
              </Box>
            ))}
          </Box>
        </Box>
        <Box sx={{ gridColumn: "1 / -1" }}>
          <Meta label={t("tokens.note")} value={token.note || t("tokens.noNote")} />
        </Box>
      </Box>
    </Card>
  );
}
