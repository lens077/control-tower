import { beforeEach, describe, expect, test } from "vite-plus/test";
import {
  clearTokens,
  decodeJwtPayload,
  getAccessToken,
  getExpiresAt,
  getRefreshToken,
  hasToken,
  setTokens,
} from "@/auth";
import { EMPTY_ACCOUNT, userStore } from "@/store/users";

function base64Url(value: unknown): string {
  return btoa(String.fromCharCode(...new TextEncoder().encode(JSON.stringify(value))))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

function token(claims: Record<string, unknown>): string {
  return `${base64Url({ alg: "RS256", typ: "JWT" })}.${base64Url(claims)}.signature`;
}

const claims = {
  id: "admin-1",
  name: "alice",
  displayName: "配置管理员",
  email: "alice@example.com",
  exp: Math.floor(Date.now() / 1000) + 3600,
};

describe("浏览器令牌状态", () => {
  beforeEach(() => {
    clearTokens();
    localStorage.clear();
    sessionStorage.clear();
  });

  test("令牌只保存在内存中", () => {
    setTokens({
      accessToken: token(claims),
      refreshToken: "refresh-token",
      idToken: "id-token",
      expiresAt: Date.now() + 7200_000,
    });

    expect(hasToken()).toBe(true);
    expect(getAccessToken()).toContain(".signature");
    expect(getRefreshToken()).toBe("refresh-token");
    expect(getExpiresAt()).toBe(claims.exp * 1000);
    expect(localStorage.getItem("token")).toBeNull();
  });

  test("用户资料随令牌派生且不会写入本地存储", () => {
    setTokens({ accessToken: token(claims) });

    expect(userStore.account.id).toBe("admin-1");
    expect(userStore.account.displayName).toBe("配置管理员");
    expect(localStorage.getItem("user")).toBeNull();

    clearTokens();
    expect(userStore.account).toEqual(EMPTY_ACCOUNT);
  });

  test("支持 UTF-8 claim，并拒绝过期令牌作为登录态", () => {
    expect(decodeJwtPayload(token(claims))?.displayName).toBe("配置管理员");
    setTokens({ accessToken: token({ ...claims, exp: 1 }) });
    expect(hasToken()).toBe(false);
  });
});
