import React, { createContext, useContext, useEffect, useState } from "react";
import { onAccessDenied, onAuthError } from "@/api/transport";
import { clearTokens, getIdToken, hasToken, subscribeToken } from "@/auth";
import { buildLogoutUrl, startLogin } from "@/auth/pkce";
import { renewSession, restoreSession, stopRenew } from "@/auth/session";
import { clearAccount } from "@/store/users";

interface AuthState {
  isAuthenticated: boolean;
  accessDenied: boolean;
}

const AuthStateContext = createContext<AuthState | undefined>(undefined);

interface AuthActionsContextType {
  setIsAuthenticated: (value: boolean) => void;
  login: () => void;
  logout: () => void;
}

const AuthActionsContext = createContext<AuthActionsContextType | undefined>(undefined);

export const AuthProvider: React.FC<{ children: React.ReactNode; router: any }> = ({
  children,
  router,
}) => {
  const [isAuthenticated, setIsAuthenticated] = useState(hasToken);
  const [accessDenied, setAccessDenied] = useState(false);
  const recoveryRef = React.useRef<Promise<void> | null>(null);
  const recoveryVersionRef = React.useRef(0);
  const mountedRef = React.useRef(false);
  const deniedRef = React.useRef(false);
  const deniedIdTokenRef = React.useRef<string | null>(null);

  const login = React.useCallback(() => {
    recoveryVersionRef.current += 1;
    recoveryRef.current = null;
    stopRenew();

    if (deniedIdTokenRef.current) {
      const currentIdToken = deniedIdTokenRef.current;
      deniedIdTokenRef.current = null;
      deniedRef.current = false;
      setAccessDenied(false);
      window.location.assign(buildLogoutUrl(`${window.location.origin}/`, currentIdToken));
      return;
    }

    const forceAuthentication = deniedRef.current;
    deniedRef.current = false;
    setAccessDenied(false);
    const currentHref = window.location.origin + router.state.location.href;
    sessionStorage.setItem("redirect_after_login", currentHref);
    void startLogin(forceAuthentication).catch((error) =>
      console.error("[Auth] 无法启动 Casdoor 登录:", error),
    );
  }, [router]);

  const logout = React.useCallback(() => {
    const currentIdToken = getIdToken();
    recoveryVersionRef.current += 1;
    recoveryRef.current = null;
    stopRenew();
    clearTokens();
    clearAccount();
    sessionStorage.removeItem("redirect_after_login");
    deniedRef.current = false;
    deniedIdTokenRef.current = null;
    setAccessDenied(false);
    setIsAuthenticated(false);

    if (currentIdToken) {
      window.location.assign(buildLogoutUrl(`${window.location.origin}/`, currentIdToken));
      return;
    }
    void router.navigate({ to: "/" });
  }, [router]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      recoveryVersionRef.current += 1;
      recoveryRef.current = null;
      stopRenew();
    };
  }, []);

  useEffect(() => {
    if (window.location.pathname.startsWith("/callback")) return;
    const recoveryVersion = recoveryVersionRef.current;
    void restoreSession().then((restored) => {
      if (!mountedRef.current || recoveryVersion !== recoveryVersionRef.current) return;
      setIsAuthenticated(restored);
      if (restored) setAccessDenied(false);
    });
  }, []);

  useEffect(
    () =>
      subscribeToken((token) => {
        setIsAuthenticated(token !== null);
        if (token) {
          deniedRef.current = false;
          setAccessDenied(false);
        }
      }),
    [],
  );

  useEffect(() => {
    const unsubscribe = onAuthError((error) => {
      if (
        window.location.pathname.startsWith("/callback") ||
        recoveryRef.current ||
        deniedRef.current
      ) {
        return;
      }

      const recoveryVersion = recoveryVersionRef.current;
      const recovery = renewSession()
        .then(() => {
          if (
            !mountedRef.current ||
            deniedRef.current ||
            recoveryVersion !== recoveryVersionRef.current
          ) {
            return;
          }
          setIsAuthenticated(true);
        })
        .catch((renewError) => {
          if (
            !mountedRef.current ||
            deniedRef.current ||
            recoveryVersion !== recoveryVersionRef.current
          ) {
            return;
          }
          console.warn("[Auth] 会话续期失败，重新登录:", renewError, "原始错误:", error);
          stopRenew();
          clearTokens();
          clearAccount();
          setIsAuthenticated(false);
          login();
        });
      recoveryRef.current = recovery;
      const release = () => {
        if (recoveryRef.current === recovery) recoveryRef.current = null;
      };
      void recovery.then(release, release);
    });
    return unsubscribe;
  }, [login]);

  useEffect(() => {
    const unsubscribe = onAccessDenied((error) => {
      console.warn("[Auth] 当前 Casdoor 账号没有 Config Center 管理权限:", error);
      deniedIdTokenRef.current = getIdToken();
      deniedRef.current = true;
      recoveryVersionRef.current += 1;
      recoveryRef.current = null;
      stopRenew();
      clearTokens();
      clearAccount();
      setIsAuthenticated(false);
      setAccessDenied(true);
    });
    return unsubscribe;
  }, []);

  const actions = React.useMemo(() => ({ setIsAuthenticated, login, logout }), [login, logout]);

  return (
    <AuthStateContext.Provider value={{ isAuthenticated, accessDenied }}>
      <AuthActionsContext.Provider value={actions}>{children}</AuthActionsContext.Provider>
    </AuthStateContext.Provider>
  );
};

export const useAuthState = () => {
  const context = useContext(AuthStateContext);
  if (!context) throw new Error("useAuthState must be used within an AuthProvider");
  return context;
};

export const useAuthActions = () => {
  const context = useContext(AuthActionsContext);
  if (!context) throw new Error("useAuthActions must be used within an AuthProvider");
  return context;
};
