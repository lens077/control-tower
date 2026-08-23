import { getRuntimeConfig } from "@/runtime-config";

const TRANSACTION_PREFIX = "config_center_oauth_transaction:";
const TRANSACTION_MAX_AGE_MS = 10 * 60_000;

interface OAuthTransaction {
  verifier: string;
  redirectUri: string;
  createdAt: number;
  silent: boolean;
}

export interface TokenResult {
  accessToken: string;
  refreshToken?: string | null;
  idToken?: string | null;
  expiresAt?: number;
}

interface TokenResponse {
  access_token?: unknown;
  refresh_token?: unknown;
  id_token?: unknown;
  expires_in?: unknown;
  error?: unknown;
  error_description?: unknown;
}

function endpoint(path: string): string {
  return `${getRuntimeConfig().casdoor.serverUrl.replace(/\/$/, "")}${path}`;
}

function randomString(bytes = 32): string {
  const value = new Uint8Array(bytes);
  crypto.getRandomValues(value);
  return btoa(String.fromCharCode(...value))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

async function sha256Base64Url(value: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value));
  return btoa(String.fromCharCode(...new Uint8Array(digest)))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

export function getRedirectUri(): string {
  const redirectPath = getRuntimeConfig().casdoor.redirectPath || "/callback";
  const redirectUri = new URL(redirectPath, window.location.origin);
  if (redirectUri.origin !== window.location.origin) {
    throw new Error("Casdoor redirectPath 必须与 Web 控制台同源");
  }
  return redirectUri.toString();
}

function transactionKey(state: string): string {
  return `${TRANSACTION_PREFIX}${state}`;
}

function pruneTransactions(now: number): void {
  for (let index = sessionStorage.length - 1; index >= 0; index -= 1) {
    const key = sessionStorage.key(index);
    if (!key?.startsWith(TRANSACTION_PREFIX)) continue;
    try {
      const transaction = JSON.parse(sessionStorage.getItem(key) ?? "") as OAuthTransaction;
      if (!transaction.createdAt || now - transaction.createdAt > TRANSACTION_MAX_AGE_MS) {
        sessionStorage.removeItem(key);
      }
    } catch {
      sessionStorage.removeItem(key);
    }
  }
}

export function discardLoginTransaction(state: string): void {
  sessionStorage.removeItem(transactionKey(state));
}

export function isSilentTransaction(state: string): boolean {
  try {
    const transaction = JSON.parse(
      sessionStorage.getItem(transactionKey(state)) ?? "",
    ) as OAuthTransaction;
    return (
      transaction.silent === true &&
      transaction.createdAt > 0 &&
      Date.now() - transaction.createdAt <= TRANSACTION_MAX_AGE_MS
    );
  } catch {
    return false;
  }
}

export async function buildLoginUrl(silent = false): Promise<string> {
  const verifier = randomString();
  const state = randomString(16);
  const redirectUri = getRedirectUri();

  const now = Date.now();
  pruneTransactions(now);
  sessionStorage.setItem(
    transactionKey(state),
    JSON.stringify({ verifier, redirectUri, createdAt: now, silent } satisfies OAuthTransaction),
  );

  const parameters = new URLSearchParams({
    client_id: getRuntimeConfig().casdoor.clientId,
    response_type: "code",
    redirect_uri: redirectUri,
    scope: "openid profile email",
    state,
    code_challenge: await sha256Base64Url(verifier),
    code_challenge_method: "S256",
  });
  if (silent) parameters.set("prompt", "none");
  return `${endpoint("/login/oauth/authorize")}?${parameters.toString()}`;
}

export async function startLogin(forceAuthentication = false): Promise<void> {
  const url = new URL(await buildLoginUrl());
  if (forceAuthentication) url.searchParams.set("prompt", "login");
  window.location.assign(url);
}

export function buildLogoutUrl(postLogoutRedirectUri: string, currentIdToken: string): string {
  const parameters = new URLSearchParams({
    post_logout_redirect_uri: postLogoutRedirectUri,
    id_token_hint: currentIdToken,
  });
  return `${endpoint("/api/logout")}?${parameters.toString()}`;
}

async function parseTokenResponse(
  response: Response,
  preserveMissingTokens: boolean,
): Promise<TokenResult> {
  let payload: TokenResponse;
  try {
    payload = (await response.json()) as TokenResponse;
  } catch {
    throw new Error(
      `Casdoor 令牌端点返回 ${response.status} ${response.statusText || "非 JSON 响应"}`,
    );
  }
  if (!payload || typeof payload !== "object") {
    throw new Error(`Casdoor 令牌端点返回 ${response.status} 非对象 JSON`);
  }
  if (!response.ok || payload.error) {
    const detail = payload.error_description ?? payload.error;
    const message = typeof detail === "string" ? detail : `HTTP ${response.status}`;
    throw new Error(`令牌兑换失败: ${message}`);
  }
  if (typeof payload.access_token !== "string" || !payload.access_token) {
    throw new Error("Casdoor 响应缺少 access_token");
  }

  let expiresAt: number | undefined;
  if (payload.expires_in !== undefined) {
    const expiresIn =
      typeof payload.expires_in === "number" || typeof payload.expires_in === "string"
        ? Number(payload.expires_in)
        : Number.NaN;
    if (!Number.isFinite(expiresIn) || expiresIn <= 0) {
      throw new Error("Casdoor 响应包含无效的 expires_in");
    }
    expiresAt = Date.now() + expiresIn * 1000;
  }

  return {
    accessToken: payload.access_token,
    refreshToken:
      typeof payload.refresh_token === "string"
        ? payload.refresh_token
        : preserveMissingTokens
          ? undefined
          : null,
    idToken:
      typeof payload.id_token === "string"
        ? payload.id_token
        : preserveMissingTokens
          ? undefined
          : null,
    expiresAt,
  };
}

export async function exchangeCode(code: string, state: string): Promise<TokenResult> {
  const key = transactionKey(state);
  const stored = sessionStorage.getItem(key);
  sessionStorage.removeItem(key);
  if (!stored) {
    throw new Error("OAuth state 校验失败，可能是 CSRF 或登录会话已过期");
  }

  let transaction: OAuthTransaction;
  try {
    transaction = JSON.parse(stored) as OAuthTransaction;
  } catch {
    throw new Error("OAuth 登录事务损坏");
  }
  if (
    !transaction.verifier ||
    !transaction.redirectUri ||
    !transaction.createdAt ||
    Date.now() - transaction.createdAt > TRANSACTION_MAX_AGE_MS
  ) {
    throw new Error("OAuth 登录事务已过期或不完整");
  }

  const body = new URLSearchParams({
    grant_type: "authorization_code",
    client_id: getRuntimeConfig().casdoor.clientId,
    code,
    redirect_uri: transaction.redirectUri,
    code_verifier: transaction.verifier,
  });
  const response = await fetch(endpoint("/api/login/oauth/access_token"), {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body,
  });
  return parseTokenResponse(response, false);
}

export async function refreshTokens(currentRefreshToken: string): Promise<TokenResult> {
  const body = new URLSearchParams({
    grant_type: "refresh_token",
    client_id: getRuntimeConfig().casdoor.clientId,
    refresh_token: currentRefreshToken,
    scope: "openid profile email",
  });
  const response = await fetch(endpoint("/api/login/oauth/refresh_token"), {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body,
  });
  // RFC 6749 section 6: when a refresh response omits a new refresh token,
  // the client keeps using the existing one. Code exchanges do not preserve it.
  return parseTokenResponse(response, true);
}

export function silentRenew(timeoutMs = 8000): Promise<TokenResult> {
  return new Promise((resolve, reject) => {
    let settled = false;
    let pendingState: string | null = null;
    const iframe = document.createElement("iframe");
    iframe.style.display = "none";

    const cleanup = () => {
      window.removeEventListener("message", onMessage);
      clearTimeout(timer);
      iframe.remove();
    };
    const finish = (callback: () => void) => {
      if (settled) return;
      settled = true;
      cleanup();
      callback();
    };
    const onMessage = (event: MessageEvent) => {
      if (event.origin !== window.location.origin || event.source !== iframe.contentWindow) return;
      const payload = event.data as {
        type?: string;
        code?: string;
        state?: string;
        error?: string;
      };
      if (payload?.type !== "config_center_oauth_silent_result") return;
      if (!pendingState || payload.state !== pendingState || !isSilentTransaction(pendingState)) {
        return;
      }
      if (payload.error || !payload.code) {
        discardLoginTransaction(pendingState);
        finish(() => reject(new Error(payload.error || "静默续期未返回授权码")));
        return;
      }
      const code = payload.code;
      const state = pendingState;
      finish(() => void exchangeCode(code, state).then(resolve, reject));
    };
    const timer = window.setTimeout(() => {
      if (pendingState) discardLoginTransaction(pendingState);
      finish(() => reject(new Error("静默续期超时")));
    }, timeoutMs);

    window.addEventListener("message", onMessage);
    void buildLoginUrl(true).then(
      (url) => {
        const state = new URL(url).searchParams.get("state");
        if (settled) {
          if (state) discardLoginTransaction(state);
          return;
        }
        if (!state) {
          finish(() => reject(new Error("静默授权 URL 缺少 state")));
          return;
        }
        pendingState = state;
        iframe.src = url;
        document.body.appendChild(iframe);
      },
      (error) => finish(() => reject(error)),
    );
  });
}
