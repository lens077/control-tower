import { createFileRoute, Outlet } from "@tanstack/react-router";
import { Box } from "@mui/material";
import { Explorer } from "@/components/Explorer";

/**
 * 配置工作区:浏览(/)、编辑(/edit)、历史(/history)共用一个壳 ——
 * 左侧资源栏常驻,切 key、切环境不丢上下文;URL 契约保持原样(pathless layout)。
 */
export const Route = createFileRoute("/_workspace")({
  component: WorkspaceLayout,
});

function WorkspaceLayout() {
  return (
    <Box
      sx={{
        flex: 1,
        minHeight: 0,
        display: "flex",
        flexDirection: { xs: "column", md: "row" },
      }}
    >
      <Explorer />
      <Box
        component="section"
        sx={{ flex: 1, minWidth: 0, minHeight: 0, display: "flex", flexDirection: "column" }}
      >
        <Outlet />
      </Box>
    </Box>
  );
}
