import { clearTokens, getExpiresAt, getRefreshToken, hasToken, setTokens } from "@/auth";
import { refreshTokens, silentRenew, type TokenResult } from "@/auth/pkce";

const RENEW_LEAD_MS = 60_000;
const RENEW_RETRY_MS = 15_000;

let renewTimer: ReturnType<typeof setTimeout> | null = null;
let inflight: Promise<TokenResult> | null = null;
let generation = 0;

function applyTokens(tokens: TokenResult, expectedGeneration: number): TokenResult {
  if (generation !== expectedGeneration) {
    throw new Error("会话续期已取消");
  }
  setTokens(tokens);
  scheduleRenew();
  return tokens;
}

export function renewSession(): Promise<TokenResult> {
  if (inflight) return inflight;

  const expectedGeneration = generation;
  const request = (async () => {
    const currentRefreshToken = getRefreshToken();
    if (currentRefreshToken) {
      try {
        return applyTokens(await refreshTokens(currentRefreshToken), expectedGeneration);
      } catch (error) {
        if (generation !== expectedGeneration) throw error;
        console.warn("[Auth] refresh_token 续期失败，回退到静默授权:", error);
      }
    }
    return applyTokens(await silentRenew(), expectedGeneration);
  })();

  inflight = request;
  const release = () => {
    if (inflight === request) inflight = null;
  };
  void request.then(release, release);
  return request;
}

function clearExpiredSession(expectedGeneration: number): void {
  renewTimer = null;
  if (generation !== expectedGeneration) return;

  const remaining = getExpiresAt() - Date.now();
  if (remaining > 0) {
    renewTimer = setTimeout(
      () => clearExpiredSession(expectedGeneration),
      Math.max(remaining, 1000),
    );
    return;
  }
  clearTokens();
}

function runScheduledRenew(expectedGeneration: number): void {
  renewTimer = null;
  void renewSession().catch((error) => {
    if (generation !== expectedGeneration) return;
    console.warn("[Auth] 定时续期失败:", error);

    const remaining = getExpiresAt() - Date.now();
    if (remaining <= RENEW_RETRY_MS) {
      renewTimer = setTimeout(
        () => clearExpiredSession(expectedGeneration),
        Math.max(remaining, 1),
      );
      return;
    }
    renewTimer = setTimeout(() => runScheduledRenew(expectedGeneration), RENEW_RETRY_MS);
  });
}

export function scheduleRenew(): void {
  if (renewTimer) clearTimeout(renewTimer);
  const expiry = getExpiresAt();
  if (!expiry) return;

  const delay = Math.max(expiry - Date.now() - RENEW_LEAD_MS, 1000);
  const expectedGeneration = generation;
  renewTimer = setTimeout(() => runScheduledRenew(expectedGeneration), delay);
}

export function stopRenew(): void {
  generation += 1;
  if (renewTimer) clearTimeout(renewTimer);
  renewTimer = null;
  inflight = null;
}

export async function restoreSession(): Promise<boolean> {
  if (hasToken()) return true;
  try {
    await renewSession();
    return true;
  } catch {
    if (hasToken()) return true;
    clearTokens();
    return false;
  }
}
