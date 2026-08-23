import { useMemo } from "react";
import { Alert, Box, Card, CircularProgress, Typography } from "@mui/material";
import { LineChart } from "@mui/x-charts/LineChart";
import type { SeriesResult } from "@/gen/api";
import { formatMetricValue } from "@/lib/metric-format";
import { buildChartData } from "@/lib/metric-series";
import { sp } from "@/styles/glass";

interface Props {
  title: string;
  /** 后端返回的一组曲线。undefined 表示还在加载 */
  result?: SeriesResult;
  emptyHint: string;
  height?: number;
}

/**
 * 一张折线图。
 *
 * 三个状态各自有独立呈现,不能混:加载中转圈、查询出错显示原因、
 * 查到但没数据显示提示。把后两者都画成空图是最糟的选择 ——
 * 「查询写错了」和「服务确实没流量」会长得一模一样。
 */
export function MetricChart({ title, result, emptyHint, height = 220 }: Props) {
  const { xAxis, series, unit } = useMemo(
    () => buildChartData(result?.lines ?? []),
    [result?.lines],
  );

  return (
    <Card sx={{ p: sp[3], display: "flex", flexDirection: "column", gap: sp[2] }}>
      <Typography variant="subtitle2" sx={{ fontWeight: 700 }}>
        {title}
      </Typography>

      {!result ? (
        <Box sx={{ height, display: "grid", placeItems: "center" }}>
          <CircularProgress size={22} />
        </Box>
      ) : result.error && series.length === 0 ? (
        <Alert severity="warning" sx={{ fontSize: 13 }}>
          {result.error}
        </Alert>
      ) : series.length === 0 ? (
        <Box sx={{ height, display: "grid", placeItems: "center" }}>
          <Typography variant="body2" color="text.secondary">
            {emptyHint}
          </Typography>
        </Box>
      ) : (
        <LineChart
          height={height}
          xAxis={[
            {
              data: xAxis,
              // scaleType time 让 x 轴按真实时间间隔分布。用 point 的话
              // 采集有断点时图上会画成等距,把一段空白伪装成连续数据。
              scaleType: "time",
              valueFormatter: (value: Date) =>
                value.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }),
            },
          ]}
          yAxis={[{ valueFormatter: (value: number) => formatMetricValue(value, unit), width: 64 }]}
          series={series}
          margin={{ top: 8, right: 8, bottom: 4, left: 4 }}
          slotProps={{ legend: { sx: { fontSize: 12 } } }}
        />
      )}
    </Card>
  );
}
