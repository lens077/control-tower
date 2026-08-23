import { Code, ConnectError } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { getAccessToken } from "@/auth";
import { getRuntimeConfig } from "@/runtime-config";

type AuthErrorListener = (error: unknown) => void;
const authErrorListeners = new Set<AuthErrorListener>();
const accessDeniedListeners = new Set<AuthErrorListener>();

export function onAuthError(listener: AuthErrorListener): () => void {
  authErrorListeners.add(listener);
  return () => authErrorListeners.delete(listener);
}

export function onAccessDenied(listener: AuthErrorListener): () => void {
  accessDeniedListeners.add(listener);
  return () => accessDeniedListeners.delete(listener);
}

const authenticatedFetch: typeof fetch = async (input, init) => {
  const headers = new Headers(init?.headers);
  const token = getAccessToken();
  if (token) headers.set("Authorization", `Bearer ${token}`);

  const response = await fetch(input, { ...init, headers });
  const responseMatchesCurrentCredential = token === getAccessToken();
  if (response.status === 401 && responseMatchesCurrentCredential) {
    for (const listener of authErrorListeners) listener(response);
  } else if (response.status === 403 && responseMatchesCurrentCredential) {
    for (const listener of accessDeniedListeners) listener(response);
  }
  return response;
};

let transport: ReturnType<typeof createConnectTransport> | undefined;

export function createAppTransport(): ReturnType<typeof createConnectTransport> {
  transport ??= createConnectTransport({
    baseUrl: getRuntimeConfig().apiUrl,
    fetch: authenticatedFetch,
  });
  return transport;
}

export function toAppError(error: unknown): { code: Code; message: string } {
  const connectError = ConnectError.from(error);
  return {
    code: connectError.code,
    message: connectError.message || "Request failed",
  };
}
