import { useMemo, useState } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  Alert,
  Box,
  Card,
  Chip,
  CircularProgress,
  Divider,
  LinearProgress,
  ToggleButton,
  ToggleButtonGroup,
  Tooltip,
  Typography,
} from "@mui/material";
import {
  Activity,
  AlertTriangle,
  Cpu,
  Database,
  HardDrive,
  MemoryStick,
  Network,
} from "lucide-react";
import type { GetSystemStatusResponse, SeriesResult } from "@/gen/api";
import { MetricSeries, systemApi, TIME_RANGES, type TimeRangeKey } from "@/api";
import { toAppError } from "@/api/transport";
import { MetricChart } from "@/components/MetricChart";
import { formatBytes, formatDuration, formatMetricValue, usageSeverity } from "@/lib/metric-format";
import { MetricUnit } from "@/gen/api";
import { useTranslation } from "@/i18n";
import { sp } from "@/styles/glass";

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

  return (
    <Box
      sx={{
        maxWidth: 1180,
        mx: "auto",
        width: "100%",
        display: "flex",
        flexDirection: "column",
        gap: sp[4],
      }}
    >
      <Header
        status={status.data}
        range={range}
        onRangeChange={setRange}
        loading={status.isLoading}
      />

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
          <SectionTitle text={t("system.section.process")} />
          <ChartGrid>
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
          </ChartGrid>

          <SectionTitle text={t("system.section.api")} hint={t("system.section.apiHint")} />
          <ChartGrid>
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
          </ChartGrid>

          <SectionTitle text={t("system.section.host")} hint={t("system.section.hostHint")} />
          <ChartGrid>
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
          </ChartGrid>
        </>
      )}
    </Box>
  );
}

// 加载中返回 undefined 让图自己转圈;加载完但后端没给这一组,
// 构造一个空结果,图会显示「无数据」而不是永远转圈。
function chart(map: Map<MetricSeries, SeriesResult>, series: MetricSeries, ready: boolean) {
  if (!ready) return undefined;
  return map.get(series) ?? ({ series, lines: [], error: "" } as unknown as SeriesResult);
}

function Header({
  status,
  range,
  onRangeChange,
  loading,
}: {
  status?: GetSystemStatusResponse;
  range: TimeRangeKey;
  onRangeChange: (value: TimeRangeKey) => void;
  loading: boolean;
}) {
  const { t } = useTranslation();
  const build = status?.build;
  const uptime = status?.process?.uptime;

  return (
    <Card sx={{ p: sp[4] }}>
      <Box
        sx={{
          display: "flex",
          flexDirection: { xs: "column", sm: "row" },
          alignItems: { sm: "center" },
          gap: sp[2],
        }}
      >
        <Box sx={{ flex: 1 }}>
          <Typography variant="h5" sx={{ fontWeight: 800 }}>
            {t("system.title")}
          </Typography>
          <Typography color="text.secondary">{t("system.subtitle")}</Typography>
        </Box>
        {loading && <CircularProgress size={18} />}
        <ToggleButtonGroup
          size="small"
          exclusive
          value={range}
          onChange={(_, value) => value && onRangeChange(value as TimeRangeKey)}
        >
          {RANGE_KEYS.map((key) => (
            <ToggleButton key={key} value={key}>
              {key}
            </ToggleButton>
          ))}
        </ToggleButtonGroup>
      </Box>

      {build && (
        <Box sx={{ display: "flex", flexWrap: "wrap", gap: sp[1], mt: sp[3] }}>
          <Chip size="small" variant="outlined" label={build.serviceName} />
          <Chip size="small" variant="outlined" label={build.version} />
          <Chip size="small" variant="outlined" label={build.environment} />
          <Chip size="small" variant="outlined" label={build.goVersion} />
          {uptime && (
            <Chip
              size="small"
              variant="outlined"
              label={t("system.uptime", { value: formatDuration(Number(uptime.seconds)) })}
            />
          )}
        </Box>
      )}
    </Card>
  );
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
        <Alert severity="info" icon={<AlertTriangle size={18} />}>
          {t("system.hostScopeLimits")}
        </Alert>
      )}

      <Box
        sx={{
          display: "grid",
          gridTemplateColumns: { xs: "1fr", sm: "1fr 1fr", lg: "repeat(4, 1fr)" },
          gap: sp[3],
        }}
      >
        <StatCard
          icon={<Cpu size={18} />}
          label={t("system.card.cpu")}
          value={`${process.cpuPercent.toFixed(1)}%`}
          caption={t("system.card.cpuCaption", { cores: process.cpuLimitCores })}
          percent={process.cpuPercent}
          degraded={degraded.has("cpu")}
        />
        <StatCard
          icon={<MemoryStick size={18} />}
          label={t("system.card.memory")}
          value={formatBytes(Number(process.memoryRssBytes))}
          caption={`/ ${formatBytes(Number(process.memoryLimitBytes))}`}
          percent={memoryPercent}
          degraded={degraded.has("memory")}
        />
        <StatCard
          icon={<HardDrive size={18} />}
          label={t("system.card.disk")}
          value={formatBytes(Number(process.diskUsedBytes))}
          caption={`/ ${formatBytes(Number(process.diskTotalBytes))} · ${process.diskPath}`}
          percent={diskPercent}
          degraded={degraded.has("disk")}
        />
        <StatCard
          icon={<Network size={18} />}
          label={t("system.card.network")}
          value={`↓ ${formatMetricValue(process.netRxBytesPerSec, MetricUnit.BYTES_PER_SECOND)}`}
          caption={`↑ ${formatMetricValue(process.netTxBytesPerSec, MetricUnit.BYTES_PER_SECOND)}`}
          degraded={degraded.has("network")}
        />
      </Box>

      {process.degraded.length > 0 && (
        <Alert severity="warning">
          {t("system.degraded")}
          <Box component="ul" sx={{ m: 0, pl: sp[4] }}>
            {process.degraded.map((item) => (
              <li key={item}>
                <Typography variant="caption" sx={{ fontFamily: "monospace" }}>
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

function StatCard({
  icon,
  label,
  value,
  caption,
  percent,
  degraded,
}: {
  icon: React.ReactNode;
  label: string;
  value: string;
  caption: string;
  percent?: number;
  degraded?: boolean;
}) {
  const { t } = useTranslation();
  const severity = percent === undefined ? "success" : usageSeverity(percent);

  const card = (
    <Card
      sx={{
        p: sp[3],
        display: "flex",
        flexDirection: "column",
        gap: sp[1],
        opacity: degraded ? 0.5 : 1,
      }}
    >
      <Box sx={{ display: "flex", alignItems: "center", gap: sp[1], color: "text.secondary" }}>
        {icon}
        <Typography variant="caption">{label}</Typography>
      </Box>
      <Typography variant="h5" sx={{ fontWeight: 700 }}>
        {degraded ? "—" : value}
      </Typography>
      <Typography variant="caption" color="text.secondary">
        {degraded ? t("system.notSampled") : caption}
      </Typography>
      {percent !== undefined && !degraded && (
        <LinearProgress
          variant="determinate"
          value={Math.min(100, Math.max(0, percent))}
          color={severity}
          sx={{ height: 6, borderRadius: 3, mt: sp[1] }}
        />
      )}
    </Card>
  );

  // 采样失败的卡片标灰并给出解释。显示 0 是错的 ——
  // 「没采到」和「真的是 0」在页面上必须能区分开。
  return degraded ? <Tooltip title={t("system.notSampledHint")}>{card}</Tooltip> : card;
}

function Dependencies({ status }: { status: GetSystemStatusResponse }) {
  const { t } = useTranslation();
  if (status.dependencies.length === 0) return null;

  return (
    <Card sx={{ p: sp[3] }}>
      <Box sx={{ display: "flex", alignItems: "center", gap: sp[1], mb: sp[2] }}>
        <Database size={18} />
        <Typography variant="subtitle2" sx={{ fontWeight: 700 }}>
          {t("system.dependencies")}
        </Typography>
      </Box>
      <Divider />
      <Box sx={{ display: "flex", flexWrap: "wrap", gap: sp[2], mt: sp[2] }}>
        {status.dependencies.map((dep) => (
          <Tooltip key={dep.name} title={dep.detail || ""}>
            <Chip
              size="small"
              icon={<Activity size={14} />}
              color={dep.healthy ? "success" : "error"}
              label={`${dep.name}: ${dep.healthy ? t("system.healthy") : t("system.unhealthy")}`}
            />
          </Tooltip>
        ))}
      </Box>
    </Card>
  );
}

function SectionTitle({ text, hint }: { text: string; hint?: string }) {
  return (
    <Box>
      <Typography variant="subtitle1" sx={{ fontWeight: 800 }}>
        {text}
      </Typography>
      {hint && (
        <Typography variant="caption" color="text.secondary">
          {hint}
        </Typography>
      )}
    </Box>
  );
}

function ChartGrid({ children }: { children: React.ReactNode }) {
  return (
    <Box sx={{ display: "grid", gridTemplateColumns: { xs: "1fr", md: "1fr 1fr" }, gap: sp[3] }}>
      {children}
    </Box>
  );
}

function ratio(used: number, total: number): number | undefined {
  if (!Number.isFinite(used) || !Number.isFinite(total) || total <= 0) return undefined;
  return (used / total) * 100;
}
