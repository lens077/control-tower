import CheckIcon from "@mui/icons-material/Check";
import { CircularProgress } from "@mui/material";
import Alert from "@mui/material/Alert";
import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useEffect, useRef, useState } from "react";
import { flushSync } from "react-dom";
import { z } from "zod";
import { setTokens } from "@/auth";
import { discardLoginTransaction, exchangeCode, isSilentTransaction } from "@/auth/pkce";
import { scheduleRenew } from "@/auth/session";
import { i18next, useTranslation } from "@/i18n";
import { useAuthActions } from "@/providers/AuthProvider";

const CallbackSearchSchema = z.object({
  code: z.string().min(1).optional(),
  state: z.string().min(1, "缺少 state 参数"),
  error: z.string().optional(),
  error_description: z.string().optional(),
});

type Status = "loading" | "success" | "error";

export const Route = createFileRoute("/callback/")({
  component: RouteComponent,
  validateSearch: CallbackSearchSchema,
});

function RouteComponent() {
  const [status, setStatus] = useState<Status>("loading");
  const processedRef = useRef(false);
  const { code, state, error, error_description: errorDescription } = Route.useSearch();
  const navigate = useNavigate();
  const { setIsAuthenticated } = useAuthActions();
  const { t } = useTranslation();

  useEffect(() => {
    if (processedRef.current) return;
    processedRef.current = true;

    const handleLogin = async () => {
      if (window.parent !== window) {
        if (isSilentTransaction(state)) {
          window.parent.postMessage(
            {
              type: "config_center_oauth_silent_result",
              code,
              state,
              error: errorDescription || error || (!code ? "静默授权未返回 code" : undefined),
            },
            window.location.origin,
          );
        } else {
          setStatus("error");
          console.error("拒绝从非静默 OAuth 事务向父窗口转发授权码");
        }
        return;
      }

      try {
        if (error || !code) {
          discardLoginTransaction(state);
          throw new Error(errorDescription || error || "OAuth 回调缺少授权码");
        }
        const tokens = await exchangeCode(code, state);
        setTokens(tokens);
        scheduleRenew();

        flushSync(() => setIsAuthenticated(true));
        setStatus("success");

        const savedTarget = sessionStorage.getItem("redirect_after_login");
        sessionStorage.removeItem("redirect_after_login");
        if (savedTarget) {
          const target = new URL(savedTarget, window.location.origin);
          if (target.origin === window.location.origin) {
            await navigate({ to: `${target.pathname}${target.search}${target.hash}` });
            return;
          }
        }
        await navigate({ to: "/" });
      } catch (error) {
        setStatus("error");
        console.error("OAuth 回调处理失败:", error);
        await navigate({ to: "/" });
      }
    };

    void handleLogin();
  }, [code, error, errorDescription, state, navigate, setIsAuthenticated]);

  switch (status) {
    case "success":
      return (
        <Alert icon={<CheckIcon fontSize="inherit" />} severity="success">
          {t("callback.successRedirect")}
        </Alert>
      );
    case "error":
      return <Alert severity="error">{i18next.t("config:callback.errorRedirect")}</Alert>;
    case "loading":
      return <CircularProgress />;
  }
}
