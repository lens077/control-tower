import { useMemo } from "react";
import { Alert, Box, Skeleton, Typography } from "@mui/material";
import { LineChart } from "@mui/x-charts/LineChart";
import type { SeriesResult } from "@/gen/api";
import { formatMetricValue } from "@/lib/metric-format";
import { buildChartData } from "@/lib/metric-series";
import { ground, hairline, ink, sp, state } from "@/styles/tokens";

interface Props {
  title: string;
  /** 后端返回的一组曲线。undefined 表示还在加载 */
  result?: SeriesResult;
  emptyHint: string;
  height?: number;
}

/** 曲线配色:与页面的状态色同一套,第一条永远是紫罗兰。 */
const CHART_COLORS = [state.active, state.success, state.warning, state.danger, state.info, ink.muted];

/**
 * 一张折线图。
 *
 * 三个状态各自有独立呈现,不能混:加载中占位、查询出错显示原因、
 * 查到但没数据显示提示。把后两者都画成空图是最糟的选择 ——
 * 「查询写错了」和「服务确实没流量」会长得一模一样。
 */
export function MetricChart({ title, result, emptyHint, height = 200 }: Props) {
  const { xAxis, series, unit } = useMemo(
    () => buildChartData(result?.lines ?? []),
    [result?.lines],
  );

  return (
    <Box
      sx={{
        border: hairline,
        borderRadius: "8px",
        bgcolor: ground.cloud,
        display: "flex",
        flexDirection: "column",
        minWidth: 0,
      }}
    >
      <Typography
        component="h3"
        sx={{ fontSize: 13, fontWeight: 500, color: ink.strong, px: sp[3], pt: sp[3], pb: sp[1] }}
      >
        {title}
      </Typography>

      {!result ? (
        <Box sx={{ height, px: sp[3], pb: sp[3] }}>
          <Skeleton variant="rounded" height="100%" />
        </Box>
      ) : result.error && series.length === 0 ? (
        <Alert severity="warning" sx={{ m: sp[3], mt: sp[1] }}>
          {result.error}
        </Alert>
      ) : series.length === 0 ? (
        <Box sx={{ height, display: "grid", placeItems: "center" }}>
          <Typography sx={{ fontSize: 12.5, color: ink.faint }}>{emptyHint}</Typography>
        </Box>
      ) : (
        <LineChart
          height={height}
          colors={CHART_COLORS}
          xAxis={[
            {
              data: xAxis,
              // scaleType time 让 x 轴按真实时间间隔分布。用 point 的话
              // 采集有断点时图上会画成等距,把一段空白伪装成连续数据。
              scaleType: "time",
              valueFormatter: (value: Date) =>
                value.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false }),
              height: 26,
              tickLabelStyle: { fontSize: 11, fill: ink.faint },
              disableLine: true,
            },
          ]}
          yAxis={[
            {
              valueFormatter: (value: number) => formatMetricValue(value, unit),
              width: 64,
              tickLabelStyle: { fontSize: 11, fill: ink.faint },
              disableLine: true,
              disableTicks: true,
            },
          ]}
          series={series}
          margin={{ top: 4, right: 12, bottom: 4, left: 0 }}
          grid={{ horizontal: true }}
          slotProps={{
            legend: {
              position: { vertical: "top", horizontal: "end" },
              sx: { fontSize: 11.5, color: ink.muted, gap: 12, "& .MuiChartsLegend-mark": { width: 10, height: 2, borderRadius: 1 } },
            },
          }}
          sx={{
            "& .MuiChartsAxis-tick": { stroke: ground.lineStrong },
            "& .MuiChartsGrid-line": { stroke: ground.line },
            "& .MuiLineElement-root": { strokeWidth: 1.5 },
          }}
        />
      )}
    </Box>
  );
}
