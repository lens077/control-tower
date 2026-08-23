import { useEffect, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
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
  DialogTitle,
  Divider,
  IconButton,
  Stack,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import { Copy, KeyRound, Plus, RefreshCw, ShieldX } from "lucide-react";
import { configApi } from "@/api";
import { toAppError } from "@/api/transport";
import type { MachineTokenMeta } from "@/gen/api";
import { useTranslation } from "@/i18n";
import { sp } from "@/styles/glass";

export const Route = createFileRoute("/tokens")({
  component: TokensPage,
});

type Timestamp = { seconds: bigint; nanos: number };

interface IssueForm {
  serviceName: string;
  environment: string;
  allowedNamespaces: string;
  note: string;
}

const EMPTY_ISSUE_FORM: IssueForm = {
  serviceName: "",
  environment: "",
  allowedNamespaces: "",
  note: "",
};

function formatTime(value?: Timestamp): string {
  if (!value) return "-";
  return new Date(Number(value.seconds) * 1000 + Math.floor(value.nanos / 1e6)).toLocaleString();
}

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
  const [issueOpen, setIssueOpen] = useState(initialIssueOpen);
  const [issueForm, setIssueForm] = useState<IssueForm>({ ...EMPTY_ISSUE_FORM, ...initialIssueForm });
  const [issuedToken, setIssuedToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState<MachineTokenMeta | null>(null);

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
      }),
    onSuccess: async (response) => {
      setIssuedToken(response.token);
      setIssueOpen(false);
      setIssueForm(EMPTY_ISSUE_FORM);
      await queryClient.invalidateQueries({ queryKey: ["machineTokens"] });
    },
  });

  const revokeMutation = useMutation({
    mutationFn: (id: string) => api.revokeMachineToken(id),
    onSuccess: async () => {
      setRevokeTarget(null);
      await queryClient.invalidateQueries({ queryKey: ["machineTokens"] });
    },
  });

  useEffect(() => () => setIssuedToken(null), []);

  const tokens = tokensQuery.data?.tokens ?? [];
  const issueValid = issueForm.serviceName.trim() !== "" && issueForm.environment.trim() !== "";

  const closeIssuedToken = () => {
    setIssuedToken(null);
    setCopied(false);
  };

  const copyIssuedToken = async () => {
    if (!issuedToken) return;
    await navigator.clipboard.writeText(issuedToken);
    setCopied(true);
  };

  const submitIssue = (event: FormEvent) => {
    event.preventDefault();
    if (issueValid) issueMutation.mutate();
  };

  return (
    <Box sx={{ maxWidth: 1180, mx: "auto", width: "100%", display: "flex", flexDirection: "column", gap: sp[4] }}>
      <Card sx={{ p: sp[4] }}>
        <Box sx={{ display: "flex", flexDirection: { xs: "column", sm: "row" }, alignItems: { sm: "center" }, gap: sp[2] }}>
          <Box sx={{ flex: 1 }}>
            <Typography variant="h5" sx={{ fontWeight: 800 }}>{t("tokens.title")}</Typography>
            <Typography color="text.secondary">{t("tokens.subtitle")}</Typography>
          </Box>
          <Button startIcon={<RefreshCw size={17} />} onClick={() => tokensQuery.refetch()}>{t("tokens.refresh")}</Button>
          <Button variant="contained" startIcon={<Plus size={17} />} onClick={() => setIssueOpen(true)}>{t("tokens.issue")}</Button>
        </Box>
        <Stack direction={{ xs: "column", sm: "row" }} spacing={sp[2]} sx={{ mt: sp[3] }}>
          <TextField
            size="small"
            label={t("tokens.service")}
            value={serviceName}
            onChange={(event) => setServiceName(event.target.value)}
          />
          <TextField
            size="small"
            label={t("tokens.environment")}
            value={environment}
            onChange={(event) => setEnvironment(event.target.value)}
          />
        </Stack>
      </Card>

      {tokensQuery.isLoading ? (
        <Box sx={{ p: sp[6], textAlign: "center" }}><CircularProgress /></Box>
      ) : tokensQuery.isError ? (
        <Alert severity="error">{t("tokens.loadFailed", { message: toAppError(tokensQuery.error).message })}</Alert>
      ) : tokens.length === 0 ? (
        <Card sx={{ p: sp[6], textAlign: "center" }}>
          <KeyRound size={28} opacity={0.45} />
          <Typography sx={{ mt: sp[2] }}>{t(serviceName || environment ? "tokens.emptyFiltered" : "tokens.empty")}</Typography>
        </Card>
      ) : (
        tokens.map((token) => (
          <TokenCard key={token.id} token={token} onRevoke={() => setRevokeTarget(token)} />
        ))
      )}

      <Dialog open={issueOpen} onClose={() => !issueMutation.isPending && setIssueOpen(false)} fullWidth maxWidth="sm">
        <Box component="form" onSubmit={submitIssue}>
          <DialogTitle>{t("tokens.issueDialog.title")}</DialogTitle>
          <DialogContent>
            <Stack spacing={sp[3]} sx={{ pt: sp[1] }}>
              {issueMutation.isError && <Alert severity="error">{t("tokens.issueDialog.failed", { message: toAppError(issueMutation.error).message })}</Alert>}
              <TextField
                required
                autoFocus
                label={t("tokens.service")}
                value={issueForm.serviceName}
                onChange={(event) => setIssueForm((form) => ({ ...form, serviceName: event.target.value }))}
              />
              <TextField
                required
                label={t("tokens.environment")}
                value={issueForm.environment}
                onChange={(event) => setIssueForm((form) => ({ ...form, environment: event.target.value }))}
              />
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

      <Dialog open={issuedToken !== null} onClose={closeIssuedToken} fullWidth maxWidth="sm">
        <DialogTitle>{t("tokens.issued.title")}</DialogTitle>
        <DialogContent>
          <Alert severity="warning" sx={{ mb: sp[3] }}>{t("tokens.issued.warning")}</Alert>
          <Box sx={{ display: "flex", alignItems: "center", gap: sp[1], p: sp[2], borderRadius: 1, bgcolor: "action.hover" }}>
            <Typography data-testid="issued-token" sx={{ flex: 1, minWidth: 0, overflowWrap: "anywhere", fontFamily: "monospace" }}>
              {issuedToken}
            </Typography>
            <Tooltip title={copied ? t("tokens.issued.copied") : t("tokens.issued.copy")}>
              <IconButton aria-label={t("tokens.issued.copy")} onClick={copyIssuedToken}><Copy size={18} /></IconButton>
            </Tooltip>
          </Box>
        </DialogContent>
        <DialogActions>
          <Button variant="contained" onClick={closeIssuedToken}>{t("tokens.issued.close")}</Button>
        </DialogActions>
      </Dialog>

      <Dialog open={revokeTarget !== null} onClose={() => !revokeMutation.isPending && setRevokeTarget(null)} fullWidth maxWidth="sm">
        <DialogTitle>{t("tokens.revoke.title")}</DialogTitle>
        <DialogContent>
          {revokeMutation.isError && <Alert severity="error" sx={{ mb: sp[2] }}>{t("tokens.revoke.failed", { message: toAppError(revokeMutation.error).message })}</Alert>}
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
    </Box>
  );
}

function TokenCard({ token, onRevoke }: { token: MachineTokenMeta; onRevoke: () => void }) {
  const { t } = useTranslation();
  return (
    <Card>
      <Box sx={{ p: sp[3], display: "flex", flexWrap: "wrap", alignItems: "center", gap: sp[2] }}>
        <KeyRound size={18} color={token.disabled ? "#64748b" : "#14866d"} />
        <Typography sx={{ fontFamily: "monospace", fontWeight: 700 }}>{token.serviceName}</Typography>
        <Chip size="small" variant="outlined" label={token.environment} />
        <Chip size="small" color={token.disabled ? "default" : "success"} label={t(token.disabled ? "tokens.status.revoked" : "tokens.status.active")} />
        <Box sx={{ flex: 1 }} />
        <Button color="error" startIcon={<ShieldX size={16} />} disabled={token.disabled} onClick={onRevoke}>{t("tokens.revoke.action")}</Button>
      </Box>
      <Divider />
      <Box sx={{ p: sp[3], display: "grid", gridTemplateColumns: { xs: "1fr", md: "repeat(3, 1fr)" }, gap: sp[2] }}>
        <Info label={t("tokens.createdAt")} value={formatTime(token.createdAt)} />
        <Info label={t("tokens.revokedAt")} value={formatTime(token.revokedAt)} />
        <Info label={t("tokens.lastUsedAt")} value={formatTime(token.lastUsedAt)} />
        <Box sx={{ gridColumn: { md: "span 2" } }}>
          <Typography variant="caption" color="text.secondary">{t("tokens.namespaces")}</Typography>
          <Box sx={{ display: "flex", flexWrap: "wrap", gap: sp[1], mt: sp[1] }}>
            {token.allowedNamespaces.map((namespace) => <Chip key={namespace} size="small" variant="outlined" label={namespace} />)}
          </Box>
        </Box>
        <Info label={t("tokens.note")} value={token.note || t("tokens.noNote")} />
      </Box>
    </Card>
  );
}

function Info({ label, value }: { label: string; value: string }) {
  return <Box><Typography variant="caption" color="text.secondary">{label}</Typography><Typography variant="body2">{value}</Typography></Box>;
}
