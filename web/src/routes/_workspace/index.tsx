import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useSnapshot } from "valtio";
import { useMemo } from "react";
import { Box, ButtonBase, Skeleton, Typography } from "@mui/material";
import { useTranslation } from "@/i18n";
import { configApi } from "@/api";
import { useAuthState } from "@/providers/AuthProvider";
import { editorStore, setEnvironment, setNamespace } from "@/store/editor";
import { ENV_OPTIONS } from "@/lib/format";
import { envTone, font, ground, hairline, ink, sp, state } from "@/styles/tokens";

export const Route = createFileRoute("/_workspace/")({
  component: OverviewPage,
});

/** 环境列顺序:标准环境按既定顺序在前,其余按名排序在后。 */
function orderEnvs(all: Iterable<string>): string[] {
  const set = new Set(all);
  const known = ENV_OPTIONS.filter((e) => set.has(e));
  const rest = [...set].filter((e) => !(ENV_OPTIONS as readonly string[]).includes(e)).sort();
  return [...known, ...rest];
}

function OverviewPage() {
  const { t } = useTranslation();
  const snap = useSnapshot(editorStore);
  const { isAuthenticated } = useAuthState();

  const { data, isLoading } = useQuery({
    queryKey: ["listNamespaces"],
    queryFn: ({ signal }) => configApi.listNamespaces(signal),
    enabled: isAuthenticated,
  });
  const namespaces = useMemo(() => data?.namespaces ?? [], [data]);
  const envs = useMemo(
    () => orderEnvs([...namespaces.flatMap((n) => n.environments), snap.environment].filter(Boolean)),
    [namespaces, snap.environment],
  );
  const current = namespaces.find((n) => n.namespace === snap.namespace);
  const totalKeys = namespaces.reduce((sum, n) => sum + n.keyCount, 0);

  return (
    <Box sx={{ flex: 1, minHeight: 0, overflow: "auto", p: { xs: sp[4], md: sp[6] } }}>
      <Box sx={{ maxWidth: 920 }}>
        {/* 当前位置 */}
        <Typography
          variant="h5"
          component="h1"
          sx={{ display: "flex", alignItems: "baseline", gap: sp[2], flexWrap: "wrap", fontFamily: font.mono }}
        >
          <span>{snap.namespace || t("overview.noNamespace")}</span>
          {snap.namespace && (
            <>
              <Box component="span" sx={{ color: ink.faint, fontWeight: 300 }}>
                ›
              </Box>
              <Box
                component="span"
                sx={{
                  color: envTone(snap.environment).text,
                  borderBottom: `2px solid ${envTone(snap.environment).band}`,
                  pb: "1px",
                }}
              >
                {snap.environment}
              </Box>
            </>
          )}
        </Typography>
        <Typography sx={{ mt: sp[2], color: ink.muted, fontSize: 13.5 }}>
          {!snap.namespace
            ? t("browser.emptyNoNamespace")
            : current
              ? t("overview.namespaceSummary", {
                  count: current.keyCount,
                  envs: current.environments.join(t("browser.envJoin")),
                })
              : t("overview.namespaceNew", { namespace: snap.namespace })}
        </Typography>

        {/* 命名空间 × 环境 */}
        <Box sx={{ mt: sp[8], display: "flex", alignItems: "baseline", gap: sp[3] }}>
          <Typography component="h2" sx={{ fontSize: 15, fontWeight: 500, color: ink.strong }}>
            {t("overview.matrixTitle")}
          </Typography>
          {data && (
            <Typography sx={{ fontSize: 12.5, color: ink.faint, fontVariantNumeric: "tabular-nums" }}>
              {t("overview.matrixSummary", { namespaces: namespaces.length, keys: totalKeys })}
            </Typography>
          )}
        </Box>
        <Typography sx={{ mt: sp[1], mb: sp[3], fontSize: 12.5, color: ink.muted }}>
          {t("overview.matrixHint")}
        </Typography>

        {isLoading ? (
          <Box sx={{ display: "grid", gap: sp[2] }}>
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} variant="rounded" height={32} />
            ))}
          </Box>
        ) : namespaces.length === 0 ? (
          <Typography sx={{ fontSize: 13, color: ink.muted }}>{t("browser.emptyNoNamespace")}</Typography>
        ) : (
          <Box
            component="table"
            sx={{
              borderCollapse: "separate",
              borderSpacing: 0,
              width: "100%",
              border: hairline,
              borderRadius: "8px",
              overflow: "hidden",
              "& th, & td": { textAlign: "left", fontSize: 13, height: 34, px: sp[3], borderBottom: hairline },
              "& tr:last-child td": { borderBottom: "none" },
              "& th": { bgcolor: ground.mist, color: ink.faint, fontWeight: 500, fontSize: 12 },
              "& td.env, & th.env": { textAlign: "center", width: 96, px: sp[1] },
              "& td.count, & th.count": { textAlign: "right", fontVariantNumeric: "tabular-nums", width: 88 },
            }}
          >
            <thead>
              <tr>
                <th>{t("browser.namespace")}</th>
                {envs.map((env) => (
                  <th key={env} className="env">
                    <Box
                      component="span"
                      sx={{
                        display: "inline-flex",
                        alignItems: "center",
                        gap: sp[1],
                        fontFamily: font.mono,
                        color: envTone(env).text,
                      }}
                    >
                      <Box component="span" sx={{ width: 8, height: 8, borderRadius: "50%", bgcolor: envTone(env).band }} />
                      {env}
                    </Box>
                  </th>
                ))}
                <th className="count">{t("explorer.keys")}</th>
              </tr>
            </thead>
            <tbody>
              {namespaces.map((ns) => {
                const isCurrentNs = ns.namespace === snap.namespace;
                return (
                  <tr key={ns.namespace}>
                    <td>
                      <Box
                        component="span"
                        sx={{ fontFamily: font.mono, fontSize: 12.5, fontWeight: isCurrentNs ? 500 : 400, color: ink.strong }}
                      >
                        {ns.namespace}
                      </Box>
                    </td>
                    {envs.map((env) => {
                      const has = ns.environments.includes(env);
                      const active = isCurrentNs && env === snap.environment;
                      const tone = envTone(env);
                      return (
                        <td key={env} className="env">
                          <ButtonBase
                            onClick={() => {
                              setNamespace(ns.namespace);
                              setEnvironment(env);
                            }}
                            aria-label={t("overview.selectCell", { namespace: ns.namespace, environment: env })}
                            aria-pressed={active}
                            sx={{
                              width: 64,
                              height: 24,
                              borderRadius: "4px",
                              fontSize: 12,
                              fontFamily: font.mono,
                              color: has ? tone.text : ink.faint,
                              bgcolor: active ? tone.soft : "transparent",
                              boxShadow: active ? `inset 0 0 0 1px ${tone.band}` : "none",
                              transition: "background-color 120ms ease-out, box-shadow 120ms ease-out",
                              "&:hover": { bgcolor: has ? tone.soft : ground.mist },
                            }}
                          >
                            {has ? (
                              <Box
                                component="span"
                                sx={{ width: 22, height: 3, borderRadius: 2, bgcolor: tone.band }}
                              />
                            ) : (
                              <Box component="span" sx={{ color: ground.lineStrong }}>
                                ·
                              </Box>
                            )}
                          </ButtonBase>
                        </td>
                      );
                    })}
                    <td className="count">
                      <Box component="span" sx={{ color: ink.body }}>
                        {ns.keyCount}
                      </Box>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </Box>
        )}

        {/* 起手提示 */}
        <Box
          sx={{
            mt: sp[8],
            pt: sp[4],
            borderTop: hairline,
            display: "grid",
            gridTemplateColumns: { xs: "1fr", sm: "1fr 1fr 1fr" },
            gap: sp[4],
          }}
        >
          <Step n={1} title={t("overview.step1.title")} body={t("overview.step1.body")} />
          <Step n={2} title={t("overview.step2.title")} body={t("overview.step2.body")} />
          <Step n={3} title={t("overview.step3.title")} body={t("overview.step3.body")} />
        </Box>
      </Box>
    </Box>
  );
}

function Step({ n, title, body }: { n: number; title: string; body: string }) {
  return (
    <Box sx={{ display: "flex", gap: sp[3] }}>
      <Box
        aria-hidden
        sx={{
          width: 22,
          height: 22,
          borderRadius: "50%",
          border: `1px solid ${state.active}`,
          color: state.active,
          fontSize: 12,
          display: "grid",
          placeItems: "center",
          flexShrink: 0,
          mt: "1px",
        }}
      >
        {n}
      </Box>
      <Box>
        <Typography sx={{ fontSize: 13.5, fontWeight: 500, color: ink.strong }}>{title}</Typography>
        <Typography sx={{ fontSize: 12.5, color: ink.muted, mt: "2px", lineHeight: 1.5 }}>{body}</Typography>
      </Box>
    </Box>
  );
}
