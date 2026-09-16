import { proxy } from "valtio";

// 签发后的 Machine Token 明文缓存。
//
// 后端只存 token_hash，明文仅随 IssueMachineToken 的响应回来一次。这里把它留在内存里，
// 让控制台在本次会话内可以反复查看、重复复制，避免「弹窗一关就永久丢失」。
//
// 硬约束：只驻留内存，绝不写 localStorage / sessionStorage / URL。刷新页面、关闭标签页，
// 或用户确认关闭签发弹窗后，明文即不可恢复。
export interface IssuedTokenState {
  // token id -> 明文
  plaintexts: Record<string, string>;
}

export const issuedTokenStore = proxy<IssuedTokenState>({ plaintexts: {} });

export function rememberIssuedToken(id: string, token: string): void {
  if (!id || !token) return;
  issuedTokenStore.plaintexts[id] = token;
}

export function forgetIssuedToken(id: string): void {
  delete issuedTokenStore.plaintexts[id];
}

export function forgetAllIssuedTokens(): void {
  issuedTokenStore.plaintexts = {};
}
