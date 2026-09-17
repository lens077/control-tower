import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Alert, Box, Button, Card, Chip, Skeleton, Tooltip, Typography } from "@mui/material";
import { RefreshCw, Unplug } from "lucide-react";
import { configApi, type PresenceMode } from "@/api";
import { toAppError } from "@/api/transport";
import { useTranslation } from "@/i18n";
import { fmtAbsolute, fmtRelative } from "@/lib/time";
import { Meta, PageFrame } from "@/components/PageFrame";
import { EnvBand } from "@/components/Explorer";
import { font, ground, hairline, ink, sp, state } from "@/styles/tokens";

export const Route = createFileRoute("/connections")({
  component: ConnectionsPage,
});

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
  const degraded = mode === "redis_ttl_degraded";

  return (
    <PageFrame
      title={t("connections.title")}
      subtitle={t("connections.subtitle")}
      actions={
        <>
          <Chip
            size="small"
            color={degraded ? "warning" : undefined}
            variant={degraded ? "filled" : "outlined"}
            label={t(`connections.mode.${mode}`)}
            aria-label={modeDescription(t, mode)}
          />
          <Button variant="outlined" startIcon={<RefreshCw size={14} />} onClick={() => query.refetch()}>
            {t("connections.refresh")}
          </Button>
        </>
      }
    >
      {/* 汇总:一行读完 */}
      {query.data && (
        <Box sx={{ display: "grid", gap: sp[1] }}>
          <Typography sx={{ fontSize: 13, color: ink.body, fontVariantNumeric: "tabular-nums" }}>
            {t("connections.total", { count: connections.length })}
            <Box component="span" sx={{ color: ink.faint, mx: sp[2] }}>
              ·
            </Box>
            <Box component="span" sx={{ color: watching > 0 ? state.success : ink.body }}>
              {t("connections.active", { count: watching })}
            </Box>
          </Typography>
          <Typography sx={{ fontSize: 12.5, color: degraded ? state.warning : ink.faint }}>
            {modeDescription(t, mode)}
          </Typography>
        </Box>
      )}

      {query.isLoading ? (
        <Box sx={{ display: "grid", gap: sp[2] }}>
          {[0, 1].map((i) => (
            <Skeleton key={i} variant="rounded" height={96} />
          ))}
        </Box>
      ) : query.isError ? (
        <Alert severity="error">{t("connections.loadFailed", { message: toAppError(query.error).message })}</Alert>
      ) : connections.length === 0 ? (
        <Box sx={{ py: sp[10], textAlign: "center", border: `1px dashed ${ground.lineStrong}`, borderRadius: "8px" }}>
          <Unplug size={22} color={ink.faint} />
          <Typography sx={{ mt: sp[2], fontSize: 13, color: ink.muted }}>{t("connections.empty")}</Typography>
        </Box>
      ) : (
        <Box sx={{ display: "grid", gap: sp[2] }}>
          {connections.map((connection) => (
            <Card key={`${connection.clientName}:${connection.clientInstance}`}>
              <Box sx={{ px: sp[4], py: sp[3], display: "flex", flexWrap: "wrap", alignItems: "center", gap: sp[2] }}>
                <Box
                  component="span"
                  aria-hidden
                  sx={{
                    width: 8,
                    height: 8,
                    borderRadius: "50%",
                    bgcolor: connection.watching ? state.success : state.warning,
                    boxShadow: connection.watching ? `0 0 0 3px ${state.successSoft}` : `0 0 0 3px ${state.warningSoft}`,
                  }}
                />
                <Typography sx={{ fontFamily: font.mono, fontSize: 13.5, fontWeight: 500, color: ink.strong }}>
                  {connection.clientName}
                </Typography>
                <Typography sx={{ fontFamily: font.mono, fontSize: 12.5, color: ink.faint }}>
                  {connection.clientInstance || t("connections.noInstance")}
                </Typography>
                {connection.clientVersion && (
                  <Typography sx={{ fontSize: 12, color: ink.faint }}>{connection.clientVersion}</Typography>
                )}
                <Box sx={{ flex: 1 }} />
                <Chip
                  size="small"
                  color={connection.watching ? "success" : "warning"}
                  label={connection.watching ? t("connections.watching") : t("connections.disconnected")}
                />
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
                <Meta label={t("connections.lastRead")} value={<Tooltip title={fmtAbsolute(connection.lastReadAt)}><span>{fmtRelative(connection.lastReadAt)}</span></Tooltip>} />
                <Meta label={t("connections.lastWatch")} value={<Tooltip title={fmtAbsolute(connection.lastWatchAt)}><span>{fmtRelative(connection.lastWatchAt)}</span></Tooltip>} />
                <Meta
                  label={t("connections.disconnect")}
                  value={
                    connection.lastDisconnectReason ? (
                      <Tooltip title={fmtAbsolute(connection.disconnectedAt)}>
                        <span>{connection.lastDisconnectReason}</span>
                      </Tooltip>
                    ) : (
                      "—"
                    )
                  }
                />
                <Box sx={{ gridColumn: { xs: "1 / -1", md: "auto" } }}>
                  <Typography sx={{ fontSize: 11.5, color: ink.faint, lineHeight: 1.3 }}>{t("connections.targets")}</Typography>
                  <Box sx={{ display: "flex", flexWrap: "wrap", gap: sp[1], mt: sp[1] }}>
                    {connection.targets.length === 0 && (
                      <Typography sx={{ fontSize: 13, color: ink.faint }}>—</Typography>
                    )}
                    {connection.targets.map((target) => (
                      <Box
                        key={`${target.namespace}/${target.environment}/${target.key}`}
                        sx={{
                          display: "inline-flex",
                          alignItems: "center",
                          gap: sp[1],
                          px: sp[2],
                          height: 22,
                          border: hairline,
                          borderRadius: "4px",
                          fontFamily: font.mono,
                          fontSize: 12,
                          color: ink.body,
                          bgcolor: ground.cloud,
                        }}
                      >
                        <EnvBand env={target.environment} height={11} />
                        {`${target.namespace}/${target.environment}/${target.key}`}
                      </Box>
                    ))}
                  </Box>
                </Box>
              </Box>
            </Card>
          ))}
        </Box>
      )}
    </PageFrame>
  );
}

function modeDescription(t: (key: string) => string, mode: PresenceMode): string {
  return t(`connections.modeDescription.${mode}`);
}
