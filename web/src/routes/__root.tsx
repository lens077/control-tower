import { createRootRouteWithContext, Link, Outlet, useRouterState } from "@tanstack/react-router";
import { Alert, AppBar, Box, Button, Chip, Toolbar, Typography } from "@mui/material";
import { SlidersHorizontal } from "lucide-react";
import { useTranslation } from "@/i18n";
import { LocaleSwitcher } from "@/components/LocaleSwitcher";
import { useAuthActions, useAuthState } from "@/providers/AuthProvider";
import { sp } from "@/styles/glass";

interface MyRouterContext {
  auth: {
    isAuthenticated: boolean;
    accessDenied: boolean;
    setIsAuthenticated: (v: boolean) => void;
    login: () => void;
    logout: () => void;
  };
}

export const Route = createRootRouteWithContext<MyRouterContext>()({
  component: RootLayout,
});

function RootLayout() {
  const { t } = useTranslation();
  const { isAuthenticated, accessDenied } = useAuthState();
  const { login, logout } = useAuthActions();
  const isCallback = useRouterState({
    select: (s) => s.location.pathname.startsWith("/callback"),
  });

  return (
    // 用定高而不是 minHeight:让「谁滚动」下沉到 main,
    // 编辑页才能靠 flex:1 吃满剩余高度,不用去硬编 AppBar 的 56/64px
    <Box sx={{ display: "flex", flexDirection: "column", height: "100dvh" }}>
      <AppBar position="sticky">
        <Toolbar sx={{ gap: sp[3] }}>
          <SlidersHorizontal size={22} />
          <Typography
            variant="h6"
            component={Link}
            to="/"
            sx={{ fontWeight: 700, color: "inherit", textDecoration: "none" }}
          >
            {t("app.title")}
          </Typography>
          <Chip label="Config Center" size="small" variant="outlined" sx={{ ml: sp[1] }} />
          {isAuthenticated && (
            <>
              <Button component={Link} to="/connections" color="inherit">
                {t("connections.nav")}
              </Button>
              <Button component={Link} to="/tokens" color="inherit">
                {t("tokens.nav")}
              </Button>
              <Button component={Link} to="/system" color="inherit">
                {t("system.nav")}
              </Button>
            </>
          )}
          <Box sx={{ flex: 1 }} />
          <LocaleSwitcher />
          {isAuthenticated ? (
            <Button variant="outlined" color="inherit" onClick={logout}>
              {t("app.signOut")}
            </Button>
          ) : (
            <Button variant="contained" onClick={login}>
              {t(accessDenied ? "app.switchAccount" : "app.signIn")}
            </Button>
          )}
        </Toolbar>
      </AppBar>

      <Box
        component="main"
        sx={{
          flex: 1,
          minHeight: 0,
          overflow: "auto",
          p: sp[4],
          display: "flex",
          flexDirection: "column",
        }}
      >
        {!isAuthenticated && !isCallback ? (
          accessDenied ? (
            <Alert severity="error">{t("app.accessDenied")}</Alert>
          ) : (
            <Typography color="text.secondary">{t("app.loginRequired")}</Typography>
          )
        ) : (
          <Outlet />
        )}
      </Box>
    </Box>
  );
}
