import { useMemo, useState } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  Alert,
  Box,
  LinearProgress,
  Skeleton,
  ToggleButton,
  ToggleButtonGroup,
  Tooltip,
  Typography,
} from "@mui/material";
import { AlertTriangle, Database } from "lucide-react";
import type { GetSystemStatusResponse, SeriesResult } from "@/gen/api";
import { MetricSeries, systemApi, TIME_RANGES, type TimeRangeKey } from "@/api";
import { toAppError } from "@/api/transport";
import { MetricChart } from "@/components/MetricChart";
import { formatBytes, formatDuration, formatMetricValue, usageSeverity } from "@/lib/metric-format";
import { MetricUnit } from "@/gen/api";
import { useTranslation } from "@/i18n";
import { PageFrame } from "@/components/PageFrame";
import { font, hairline, ink, sp, state } from "@/styles/tokens";

export const Route = createFileRoute("/system")({
  component: SystemPage,
});

// 一次要哪些曲线。顺序即页面上的排列顺序。
//
// 全部塞进一个请求而不是每张图一个请求:后端顺序执行这几条查询,
// 但只往返一次 —— 分开发的话浏览器的并发上限会让最后几张图排队,
// 而且每个请求都要重跑一遍 IAM 的 JWT 验签。
const SERIES: MetricSeries[] = [
  MetricSeries.PROCESS_CPU,
  MetricSeries.PROCESS_MEMORY,
  MetricSeries.PROCESS_GOROUTINES,
  MetricSeries.PROCESS_NETWORK,
  MetricSeries.API_LATENCY,
  MetricSeries.API_THROUGHPUT,
  MetricSeries.API_ERROR_RATE,
  MetricSeries.HOST_CPU,
];

// 第二批:主机与数据库。分两批发是因为 proto 限制单次最多 8 组,
// 而且这两批的关注度不同 —— 上面那批是「配置中心自己怎么样」,
// 这批是「它脚下的机器和数据库怎么样」。
const SERIES_INFRA: MetricSeries[] = [
  MetricSeries.HOST_MEMORY,
  MetricSeries.HOST_DISK,
  MetricSeries.HOST_NETWORK,
  MetricSeries.DB_LATENCY,
  MetricSeries.DB_POOL,
];

const RANGE_KEYS = Object.keys(TIME_RANGES) as TimeRangeKey[];

function SystemPage() {
  const { t } = useTranslation();
  const [range, setRange] = useState<TimeRangeKey>("1h");

  // 即时值刷新得比曲线勤:它便宜(读一份内存里的快照 + 两次 ping),
  // 而且是这个页面上最常被盯着看的东西。
  const status = useQuery({
    queryKey: ["systemStatus"],
    queryFn: ({ signal }) => systemApi.getSystemStatus(signal),
    refetchInterval: 5_000,
  });

  const metrics = useQuery({
    queryKey: ["systemMetrics", range],
    queryFn: ({ signal }) => systemApi.queryMetrics(SERIES, range, signal),
    // 曲线的刷新周期跟着窗口走没有意义 —— 30s 足够,再勤只是徒增 VM 负载
    refetchInterval: 30_000,
  });

  const infra = useQuery({
    queryKey: ["systemMetricsInfra", range],
    queryFn: ({ signal }) => systemApi.queryMetrics(SERIES_INFRA, range, signal),
    refetchInterval: 30_000,
  });

  const byS = useMemo(() => {
    const map = new Map<MetricSeries, SeriesResult>();
    for (const r of metrics.data?.results ?? []) map.set(r.series, r);
    for (const r of infra.data?.results ?? []) map.set(r.series, r);
    return map;
  }, [metrics.data, infra.data]);

  const chartsReady = !metrics.isLoading && !infra.isLoading;
  const metricsAvailable = metrics.data?.metricsBackendAvailable ?? true;

  const build = status.data?.build;
  const uptime = status.data?.process?.uptime;

  return (
    <PageFrame
      title={t("system.title")}
      subtitle={t("system.subtitle")}
      actions={
        <ToggleButtonGroup
          size="small"
          exclusive
          value={range}
          onChange={(_, value) => value && setRange(value as TimeRangeKey)}
          aria-label={t("system.range")}
        >
          {RANGE_KEYS.map((key) => (
            <ToggleButton key={key} value={key} sx={{ fontFamily: font.mono }}>
              {key}
            </ToggleButton>
          ))}
        </ToggleButtonGroup>
      }
    >
      {/* 构建信息:一行 mono,不做成 chip */}
      {status.isLoading ? (
        <Skeleton variant="text" width={420} />
      ) : build ? (
        <Typography
          sx={{
            fontFamily: font.mono,
            fontSize: 12.5,
            color: ink.muted,
            display: "flex",
            flexWrap: "wrap",
            gap: sp[3],
            "& b": { fontWeight: 500, color: ink.body },
          }}
        >
          <span>
            <b>{build.serviceName}</b> {build.version}
          </span>
          <span>{build.environment}</span>
          <span>{build.goVersion}</span>
          {uptime && <span>{t("system.uptime", { value: formatDuration(Number(uptime.seconds)) })}</span>}
        </Typography>
      ) : null}

      {status.isError ? (
        <Alert severity="error">
          {t("system.loadFailed", { message: toAppError(status.error).message })}
        </Alert>
      ) : (
        <>
          {status.data?.process && <InstantCards process={status.data.process} />}
          {status.data && <Dependencies status={status.data} />}
        </>
      )}

      {!metricsAvailable ? (
        // 不画空图。空图与「一切正常但没流量」长得一模一样,
        // 而这里的真相是「根本没接指标后端」,必须说出来。
        <Alert severity="info">{t("system.metricsUnavailable")}</Alert>
      ) : (
        <>
          <Section title={t("system.section.process")}>
            <MetricChart
              title={t("system.chart.processCpu")}
              result={chart(byS, MetricSeries.PROCESS_CPU, chartsReady)}
              emptyHint={t("system.noData")}
            />
            <MetricChart
              title={t("system.chart.processMemory")}
              result={chart(byS, MetricSeries.PROCESS_MEMORY, chartsReady)}
              emptyHint={t("system.noData")}
            />
            <MetricChart
              title={t("system.chart.goroutines")}
              result={chart(byS, MetricSeries.PROCESS_GOROUTINES, chartsReady)}
              emptyHint={t("system.noData")}
            />
            <MetricChart
              title={t("system.chart.processNetwork")}
              result={chart(byS, MetricSeries.PROCESS_NETWORK, chartsReady)}
              emptyHint={t("system.noData")}
            />
          </Section>

          <Section title={t("system.section.api")} hint={t("system.section.apiHint")}>
            <MetricChart
              title={t("system.chart.apiLatency")}
              result={chart(byS, MetricSeries.API_LATENCY, chartsReady)}
              emptyHint={t("system.noTraffic")}
            />
            <MetricChart
              title={t("system.chart.apiThroughput")}
              result={chart(byS, MetricSeries.API_THROUGHPUT, chartsReady)}
              emptyHint={t("system.noTraffic")}
            />
            <MetricChart
              title={t("system.chart.apiErrorRate")}
              result={chart(byS, MetricSeries.API_ERROR_RATE, chartsReady)}
              emptyHint={t("system.noTraffic")}
            />
            <MetricChart
              title={t("system.chart.dbLatency")}
              result={chart(byS, MetricSeries.DB_LATENCY, chartsReady)}
              emptyHint={t("system.noTraffic")}
            />
            <MetricChart
              title={t("system.chart.dbPool")}
              result={chart(byS, MetricSeries.DB_POOL, chartsReady)}
              emptyHint={t("system.noData")}
            />
          </Section>

          <Section title={t("system.section.host")} hint={t("system.section.hostHint")}>
            <MetricChart
              title={t("system.chart.hostCpu")}
              result={chart(byS, MetricSeries.HOST_CPU, chartsReady)}
              emptyHint={t("system.noData")}
            />
            <MetricChart
              title={t("system.chart.hostMemory")}
              result={chart(byS, MetricSeries.HOST_MEMORY, chartsReady)}
              emptyHint={t("system.noData")}
            />
            <MetricChart
              title={t("system.chart.hostDisk")}
              result={chart(byS, MetricSeries.HOST_DISK, chartsReady)}
              emptyHint={t("system.noData")}
            />
            <MetricChart
              title={t("system.chart.hostNetwork")}
              result={chart(byS, MetricSeries.HOST_NETWORK, chartsReady)}
              emptyHint={t("system.noData")}
            />
          </Section>
        </>
      )}
    </PageFrame>
  );
}

// 加载中返回 undefined 让图自己转圈;加载完但后端没给这一组,
// 构造一个空结果,图会显示「无数据」而不是永远转圈。
function chart(map: Map<MetricSeries, SeriesResult>, series: MetricSeries, ready: boolean) {
  if (!ready) return undefined;
  return map.get(series) ?? ({ series, lines: [], error: "" } as unknown as SeriesResult);
}

function InstantCards({ process }: { process: NonNullable<GetSystemStatusResponse["process"]> }) {
  const { t } = useTranslation();

  const memoryPercent = ratio(Number(process.memoryRssBytes), Number(process.memoryLimitBytes));
  const diskPercent = ratio(Number(process.diskUsedBytes), Number(process.diskTotalBytes));
  const degraded = new Set(process.degraded.map((item) => item.split(":")[0]));

  return (
    <>
      {/* 限额没读到时必须说出来。开发机上分母是整机规格,那里看到的 0.3%
          与生产上同一个进程的表现毫无关系,不标注的话会被当成性能结论。 */}
      {!process.limitsFromCgroup && (
        <Alert severity="info" icon={<AlertTriangle size={16} />}>
          {t("system.hostScopeLimits")}
        </Alert>
      )}

      {/* 即时读数:一张四行的读数表 —— 项目 · 当前值 · 分母 · 占比条,不是四张卡片 */}
      <Box
        component="table"
        sx={{
          borderCollapse: "separate",
          borderSpacing: 0,
          width: "100%",
          border: hairline,
          borderRadius: "8px",
          "& td": { px: sp[3], height: 36, borderBottom: hairline, fontSize: 13, verticalAlign: "middle" },
          "& tr:last-child td": { borderBottom: "none" },
        }}
      >
        <tbody>
          <StatRow
            label={t("system.card.cpu")}
            value={`${process.cpuPercent.toFixed(1)}%`}
            caption={t("system.card.cpuCaption", { cores: process.cpuLimitCores })}
            percent={process.cpuPercent}
            degraded={degraded.has("cpu")}
          />
          <StatRow
            label={t("system.card.memory")}
            value={formatBytes(Number(process.memoryRssBytes))}
            caption={`/ ${formatBytes(Number(process.memoryLimitBytes))}`}
            percent={memoryPercent}
            degraded={degraded.has("memory")}
          />
          <StatRow
            label={t("system.card.disk")}
            value={formatBytes(Number(process.diskUsedBytes))}
            caption={`/ ${formatBytes(Number(process.diskTotalBytes))} · ${process.diskPath}`}
            percent={diskPercent}
            degraded={degraded.has("disk")}
          />
          <StatRow
            label={t("system.card.network")}
            value={`↓ ${formatMetricValue(process.netRxBytesPerSec, MetricUnit.BYTES_PER_SECOND)}`}
            caption={`↑ ${formatMetricValue(process.netTxBytesPerSec, MetricUnit.BYTES_PER_SECOND)}`}
            degraded={degraded.has("network")}
          />
        </tbody>
      </Box>

      {process.degraded.length > 0 && (
        <Alert severity="warning">
          {t("system.degraded")}
          <Box component="ul" sx={{ m: 0, pl: sp[4] }}>
            {process.degraded.map((item) => (
              <li key={item}>
                <Typography variant="caption" sx={{ fontFamily: font.mono, color: "inherit" }}>
                  {item}
                </Typography>
              </li>
            ))}
          </Box>
        </Alert>
      )}
    </>
  );
}

function StatRow({
  label,
  value,
  caption,
  percent,
  degraded,
}: {
  label: string;
  value: string;
  caption: string;
  percent?: number;
  degraded?: boolean;
}) {
  const { t } = useTranslation();
  const severity = percent === undefined ? "success" : usageSeverity(percent);
  const row = (
    <Box component="tr" sx={{ opacity: degraded ? 0.55 : 1 }}>
      <Box component="td" sx={{ width: 120, color: ink.muted }}>
        {label}
      </Box>
      <Box
        component="td"
        sx={{ width: 140, fontFamily: font.mono, fontSize: 13.5, color: ink.strong, fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}
      >
        {degraded ? "—" : value}
      </Box>
      <Box component="td" sx={{ color: ink.faint, fontSize: 12.5, whiteSpace: "nowrap" }} title={caption}>
        {degraded ? t("system.notSampled") : caption}
      </Box>
      <Box component="td" sx={{ width: "40%" }}>
        {percent !== undefined && !degraded && (
          <Box sx={{ display: "flex", alignItems: "center", gap: sp[2] }}>
            <LinearProgress
              variant="determinate"
              value={Math.min(100, Math.max(0, percent))}
              color={severity}
              sx={{ flex: 1 }}
            />
            <Box component="span" sx={{ fontSize: 11.5, color: ink.faint, fontVariantNumeric: "tabular-nums", width: 40, textAlign: "right" }}>
              {percent.toFixed(0)}%
            </Box>
          </Box>
        )}
      </Box>
    </Box>
  );

  // 采样失败的行标灰并给出解释。显示 0 是错的 ——
  // 「没采到」和「真的是 0」在页面上必须能区分开。
  return degraded ? <Tooltip title={t("system.notSampledHint")}>{row}</Tooltip> : row;
}

function Dependencies({ status }: { status: GetSystemStatusResponse }) {
  const { t } = useTranslation();
  if (status.dependencies.length === 0) return null;

  return (
    <Box sx={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: sp[3] }}>
      <Box sx={{ display: "flex", alignItems: "center", gap: sp[1], color: ink.faint }}>
        <Database size={14} />
        <Typography sx={{ fontSize: 12.5, color: ink.muted }}>{t("system.dependencies")}</Typography>
      </Box>
      {status.dependencies.map((dep) => (
        <Tooltip key={dep.name} title={dep.detail || ""}>
          <Box
            sx={{
              display: "inline-flex",
              alignItems: "center",
              gap: sp[2],
              px: sp[2],
              height: 24,
              border: hairline,
              borderRadius: "4px",
              fontSize: 12.5,
              color: ink.body,
            }}
          >
            <Box
              component="span"
              aria-hidden
              sx={{
                width: 7,
                height: 7,
                borderRadius: "50%",
                bgcolor: dep.healthy ? state.success : state.danger,
              }}
            />
            <Box component="span" sx={{ fontFamily: font.mono }}>
              {dep.name}
            </Box>
            <Box component="span" sx={{ color: dep.healthy ? state.success : state.danger }}>
              {dep.healthy ? t("system.healthy") : t("system.unhealthy")}
            </Box>
          </Box>
        </Tooltip>
      ))}
    </Box>
  );
}

function Section({ title, hint, children }: { title: string; hint?: string; children: React.ReactNode }) {
  return (
    <Box component="section" sx={{ pt: sp[3] }}>
      <Typography component="h2" sx={{ fontSize: 15, fontWeight: 500, color: ink.strong }}>
        {title}
      </Typography>
      {hint && (
        <Typography sx={{ fontSize: 12.5, color: ink.muted, mt: "2px", maxWidth: "80ch" }}>{hint}</Typography>
      )}
      <Box sx={{ mt: sp[3], display: "grid", gridTemplateColumns: { xs: "1fr", md: "1fr 1fr" }, gap: sp[3] }}>
        {children}
      </Box>
    </Box>
  );
}

function ratio(used: number, total: number): number | undefined {
  if (!Number.isFinite(used) || !Number.isFinite(total) || total <= 0) return undefined;
  return (used / total) * 100;
}
