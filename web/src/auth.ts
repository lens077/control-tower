export interface TokenClaims {
  exp?: number;
  id?: string;
  sub?: string;
  name?: string;
  displayName?: string;
  email?: string;
  avatar?: string;
  role?: string;
  isAdmin?: boolean;
}

type TokenListener = (token: string | null) => void;

let accessToken: string | null = null;
let refreshToken: string | null = null;
let idToken: string | null = null;
let expiresAt = 0;
const listeners = new Set<TokenListener>();

function decodeBase64Url(value: string): string {
  const base64 = value
    .replace(/-/g, "+")
    .replace(/_/g, "/")
    .padEnd(Math.ceil(value.length / 4) * 4, "=");
  const bytes = Uint8Array.from(atob(base64), (char) => char.charCodeAt(0));
  return new TextDecoder().decode(bytes);
}

export function decodeJwtPayload(token: string): TokenClaims | undefined {
  const parts = token.split(".");
  if (parts.length !== 3 || !parts[1]) return undefined;
  try {
    return JSON.parse(decodeBase64Url(parts[1])) as TokenClaims;
  } catch {
    return undefined;
  }
}

export function isTokenExpired(token: string): boolean {
  const claims = decodeJwtPayload(token);
  return !claims?.exp || claims.exp <= Math.floor(Date.now() / 1000);
}

export function getAccessToken(): string | null {
  return accessToken;
}

export function getRefreshToken(): string | null {
  return refreshToken;
}

export function getIdToken(): string | null {
  return idToken;
}

export function getExpiresAt(): number {
  return expiresAt;
}

export function hasToken(): boolean {
  return accessToken !== null && !isTokenExpired(accessToken);
}

export function setTokens(tokens: {
  accessToken: string;
  refreshToken?: string | null;
  idToken?: string | null;
  expiresAt?: number;
}): void {
  accessToken = tokens.accessToken;
  if (tokens.refreshToken !== undefined) refreshToken = tokens.refreshToken;
  if (tokens.idToken !== undefined) idToken = tokens.idToken;

  const claimExpiry = decodeJwtPayload(tokens.accessToken)?.exp;
  const claimExpiresAt = claimExpiry ? claimExpiry * 1000 : 0;
  expiresAt =
    tokens.expiresAt && claimExpiresAt
      ? Math.min(tokens.expiresAt, claimExpiresAt)
      : (tokens.expiresAt ?? claimExpiresAt);
  for (const listener of listeners) listener(accessToken);
}

export function clearTokens(): void {
  accessToken = null;
  refreshToken = null;
  idToken = null;
  expiresAt = 0;
  for (const listener of listeners) listener(null);
}

export function subscribeToken(listener: TokenListener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}
