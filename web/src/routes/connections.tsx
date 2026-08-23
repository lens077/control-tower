import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Alert, Box, Button, Card, Chip, CircularProgress, Divider, Typography } from "@mui/material";
import { Activity, RefreshCw, Unplug } from "lucide-react";
import { configApi, type PresenceMode } from "@/api";
import { toAppError } from "@/api/transport";
import { useTranslation } from "@/i18n";
import { sp } from "@/styles/glass";

export const Route = createFileRoute("/connections")({
  component: ConnectionsPage,
});

type Timestamp = { seconds: bigint; nanos: number };

function formatTime(value?: Timestamp): string {
  if (!value) return "-";
  return new Date(Number(value.seconds) * 1000 + Math.floor(value.nanos / 1e6)).toLocaleString();
}

function ConnectionsPage() {
  const { t } = useTranslation();
  const query = useQuery({
    queryKey: ["clientConnections"],
    queryFn: ({ signal }) => configApi.listClientConnections(signal),
    refetchInterval: 10_000,
  });
  const connections = query.data?.connections ?? [];
  const mode = query.data?.presenceMode ?? "local";
  const watching = connections.filter((connection) => connection.watching).length;

  return (
    <Box sx={{ maxWidth: 1180, mx: "auto", width: "100%", display: "flex", flexDirection: "column", gap: sp[4] }}>
      <Card sx={{ p: sp[4] }}>
        <Box sx={{ display: "flex", flexDirection: { xs: "column", sm: "row" }, alignItems: { sm: "center" }, gap: sp[2] }}>
          <Box sx={{ flex: 1 }}>
            <Typography variant="h5" sx={{ fontWeight: 800 }}>{t("connections.title")}</Typography>
            <Typography color="text.secondary">{t("connections.subtitle")}</Typography>
          </Box>
          <Chip size="small" color={mode === "redis_ttl_degraded" ? "warning" : "default"} label={t(`connections.mode.${mode}`)} />
          <Button startIcon={<RefreshCw size={17} />} onClick={() => query.refetch()}>{t("connections.refresh")}</Button>
        </Box>
        <Box sx={{ display: "flex", flexWrap: "wrap", gap: sp[1], mt: sp[3] }}>
          <Chip size="small" variant="outlined" label={t("connections.total", { count: connections.length })} />
          <Chip size="small" variant="outlined" color="success" label={t("connections.active", { count: watching })} />
        </Box>
        <Typography variant="caption" color={mode === "redis_ttl_degraded" ? "warning.main" : "text.secondary"} sx={{ display: "block", mt: sp[2] }}>
          {modeDescription(t, mode)}
        </Typography>
      </Card>

      {query.isLoading ? (
        <Box sx={{ p: sp[6], textAlign: "center" }}><CircularProgress /></Box>
      ) : query.isError ? (
        <Alert severity="error">{t("connections.loadFailed", { message: toAppError(query.error).message })}</Alert>
      ) : connections.length === 0 ? (
        <Card sx={{ p: sp[6], textAlign: "center" }}>
          <Unplug size={28} opacity={0.45} />
          <Typography sx={{ mt: sp[2] }}>{t("connections.empty")}</Typography>
        </Card>
      ) : (
        connections.map((connection) => (
          <Card key={`${connection.clientName}:${connection.clientInstance}`}>
            <Box sx={{ p: sp[3], display: "flex", flexWrap: "wrap", alignItems: "center", gap: sp[2] }}>
              <Activity size={18} color={connection.watching ? "#14866d" : "#b45309"} />
              <Typography sx={{ fontFamily: "monospace", fontWeight: 700 }}>{connection.clientName}</Typography>
              <Typography color="text.secondary" variant="body2">{connection.clientInstance || t("connections.noInstance")}</Typography>
              <Box sx={{ flex: 1 }} />
              <Chip size="small" color={connection.watching ? "success" : "warning"} label={connection.watching ? t("connections.watching") : t("connections.disconnected")} />
            </Box>
            <Divider />
            <Box sx={{ p: sp[3], display: "grid", gridTemplateColumns: { xs: "1fr", md: "repeat(3, 1fr)" }, gap: sp[2] }}>
              <Info label={t("connections.lastRead")} value={formatTime(connection.lastReadAt)} />
              <Info label={t("connections.lastWatch")} value={formatTime(connection.lastWatchAt)} />
              <Info label={t("connections.disconnect")} value={connection.lastDisconnectReason || "-"} />
              <Box sx={{ gridColumn: { md: "span 3" } }}>
                <Typography variant="caption" color="text.secondary">{t("connections.targets")}</Typography>
                <Box sx={{ display: "flex", flexWrap: "wrap", gap: sp[1], mt: sp[1] }}>
                  {connection.targets.map((target) => <Chip key={`${target.namespace}/${target.environment}/${target.key}`} size="small" variant="outlined" label={`${target.namespace}/${target.environment}/${target.key}`} />)}
                </Box>
              </Box>
            </Box>
          </Card>
        ))
      )}
    </Box>
  );
}

function modeDescription(t: (key: string) => string, mode: PresenceMode): string {
  return t(`connections.modeDescription.${mode}`);
}

function Info({ label, value }: { label: string; value: string }) {
  return <Box><Typography variant="caption" color="text.secondary">{label}</Typography><Typography variant="body2">{value}</Typography></Box>;
}
