import { createRootRouteWithContext, Link, Outlet, useRouterState } from "@tanstack/react-router";
import { Alert, Box, Button, Typography } from "@mui/material";
import { useTranslation } from "@/i18n";
import { LocaleSwitcher } from "@/components/LocaleSwitcher";
import { BrandMark } from "@/components/BrandMark";
import { useAuthActions, useAuthState } from "@/providers/AuthProvider";
import { ground, ink, sheenEdge, sp, state } from "@/styles/tokens";

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

const NAV: Array<{ to: "/" | "/connections" | "/tokens" | "/system"; key: string }> = [
  { to: "/", key: "app.nav.config" },
  { to: "/connections", key: "connections.nav" },
  { to: "/tokens", key: "tokens.nav" },
  { to: "/system", key: "system.nav" },
];

/** 「配置」这一组包含浏览、编辑、历史三条路由,导航高亮按组算。 */
function navActive(to: string, pathname: string): boolean {
  if (to === "/") return pathname === "/" || pathname.startsWith("/edit") || pathname.startsWith("/history");
  return pathname.startsWith(to);
}

function RootLayout() {
  const { t } = useTranslation();
  const { isAuthenticated, accessDenied } = useAuthState();
  const { login, logout } = useAuthActions();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const isCallback = pathname.startsWith("/callback");

  return (
    // 用定高而不是 minHeight:让「谁滚动」下沉到 main,
    // 工作区才能靠 flex:1 吃满剩余高度,不用去硬编顶栏的高度
    <Box sx={{ display: "flex", flexDirection: "column", height: "100dvh", bgcolor: ground.cloud }}>
      <Box
        component="header"
        sx={{
          flexShrink: 0,
          minHeight: 44,
          display: "flex",
          flexWrap: { xs: "wrap", md: "nowrap" },
          alignItems: "stretch",
          px: { xs: sp[3], md: sp[4] },
          gap: sp[2],
          // 顶栏下沿:2px 虹彩线,是这个世界的签名边
          ...sheenEdge(2),
        }}
      >
        <Box
          component={Link}
          to="/"
          sx={{
            display: "flex",
            alignItems: "center",
            gap: sp[2],
            color: ink.strong,
            textDecoration: "none",
            mr: sp[3],
            "&:hover": { color: ink.body },
          }}
        >
          <BrandMark size={18} />
          <Typography sx={{ fontSize: 15, fontWeight: 500, letterSpacing: "0.01em", whiteSpace: "nowrap" }}>
            {t("app.title")}
          </Typography>
          <Typography
            sx={{
              fontSize: 12,
              color: ink.faint,
              letterSpacing: "0.04em",
              display: { xs: "none", md: "block" },
            }}
          >
            Config Center
          </Typography>
        </Box>

        {isAuthenticated && (
          <Box
            component="nav"
            sx={{
              display: "flex",
              alignItems: "stretch",
              gap: sp[1],
              // 窄屏时导航掉到第二行,横向可滚,不挤扁品牌与登出
              order: { xs: 3, md: 0 },
              flexBasis: { xs: "100%", md: "auto" },
              minHeight: { xs: 40, md: "auto" },
              overflowX: "auto",
              mx: { xs: `-${sp[3]}`, md: 0 },
              px: { xs: sp[2], md: 0 },
              scrollbarWidth: "none",
              "&::-webkit-scrollbar": { display: "none" },
            }}
          >
            {NAV.map((item) => {
              const active = navActive(item.to, pathname);
              return (
                <Box
                  key={item.to}
                  component={Link}
                  to={item.to}
                  aria-current={active ? "page" : undefined}
                  sx={{
                    display: "flex",
                    alignItems: "center",
                    px: sp[2],
                    fontSize: 13,
                    whiteSpace: "nowrap",
                    fontWeight: active ? 500 : 400,
                    color: active ? ink.strong : ink.muted,
                    textDecoration: "none",
                    position: "relative",
                    transition: "color 150ms ease-out",
                    "&:hover": { color: ink.strong },
                    "&::after": {
                      content: '""',
                      position: "absolute",
                      left: sp[2],
                      right: sp[2],
                      // 停在虹彩边上方,不与它叠在一起
                      bottom: 3,
                      height: 2,
                      borderRadius: "2px 2px 0 0",
                      background: state.active,
                      opacity: active ? 1 : 0,
                      transform: active ? "scaleX(1)" : "scaleX(0.4)",
                      transition: "opacity 150ms ease-out, transform 200ms cubic-bezier(0.2, 0, 0, 1)",
                    },
                  }}
                >
                  {t(item.key)}
                </Box>
              );
            })}
          </Box>
        )}

        <Box sx={{ flex: 1 }} />
        <Box sx={{ display: "flex", alignItems: "center", gap: sp[2], minHeight: 44 }}>
          <LocaleSwitcher />
          {isAuthenticated ? (
            <Button variant="outlined" onClick={logout}>
              {t("app.signOut")}
            </Button>
          ) : (
            <Button variant="contained" onClick={login}>
              {t(accessDenied ? "app.switchAccount" : "app.signIn")}
            </Button>
          )}
        </Box>
      </Box>

      <Box
        component="main"
        sx={{
          flex: 1,
          minHeight: 0,
          display: "flex",
          flexDirection: "column",
          overflow: "hidden",
        }}
      >
        {!isAuthenticated && !isCallback ? (
          <SignedOut accessDenied={accessDenied} onLogin={login} />
        ) : (
          <Outlet />
        )}
      </Box>
    </Box>
  );
}

function SignedOut({ accessDenied, onLogin }: { accessDenied: boolean; onLogin: () => void }) {
  const { t } = useTranslation();
  return (
    <Box
      sx={{
        flex: 1,
        display: "grid",
        placeItems: "center",
        px: sp[6],
        background: `radial-gradient(60% 40% at 50% 0%, ${ground.mist} 0%, ${ground.cloud} 100%)`,
      }}
    >
      <Box sx={{ maxWidth: 420, width: "100%", textAlign: "center" }}>
        <BrandMark size={40} />
        <Typography variant="h5" sx={{ mt: sp[4], color: ink.strong }}>
          {t("app.title")}
        </Typography>
        {accessDenied ? (
          <Alert severity="error" sx={{ mt: sp[4], textAlign: "left" }}>
            {t("app.accessDenied")}
          </Alert>
        ) : (
          <Typography color="text.secondary" sx={{ mt: sp[2] }}>
            {t("app.loginRequired")}
          </Typography>
        )}
        <Button variant="contained" size="medium" onClick={onLogin} sx={{ mt: sp[5], px: sp[5] }}>
          {t(accessDenied ? "app.switchAccount" : "app.signIn")}
        </Button>
      </Box>
    </Box>
  );
}
