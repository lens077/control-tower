import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { afterEach, beforeAll, describe, expect, test, vi } from "vite-plus/test";
import { TokensPage } from "@/routes/tokens";
import {
  IssueMachineTokenResponseSchema,
  ListMachineTokensResponseSchema,
  RevokeMachineTokenResponseSchema,
} from "@/gen/api";
import { i18next, initI18n } from "@/i18n";
import configEn from "@/locales/en/config.json";
import configZh from "@/locales/zh-CN/config.json";

vi.mock("@/api", () => ({
  configApi: {
    listMachineTokens: vi.fn(),
    issueMachineToken: vi.fn(),
    revokeMachineToken: vi.fn(),
  },
}));

vi.mock("@/api/transport", () => ({
  toAppError: (error: unknown) => ({ message: error instanceof Error ? error.message : "Request failed" }),
}));

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

beforeAll(async () => {
  await initI18n({
    ns: "config",
    resources: { "zh-CN": configZh, en: configEn },
    titleKey: "app.title",
  });
  await i18next.changeLanguage("en");
});

let root: Root | undefined;
let container: HTMLDivElement | undefined;

function buildApi() {
  return {
    listMachineTokens: vi.fn().mockResolvedValue(create(ListMachineTokensResponseSchema)),
    issueMachineToken: vi.fn().mockResolvedValue(
      create(IssueMachineTokenResponseSchema, { token: "ct_once_only_plaintext" }),
    ),
    revokeMachineToken: vi.fn().mockResolvedValue(create(RevokeMachineTokenResponseSchema)),
  };
}

async function renderPage(api: ReturnType<typeof buildApi>, props: Record<string, unknown> = {}) {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  await act(async () => {
    root?.render(
      <QueryClientProvider client={queryClient}>
        <TokensPage api={api as never} {...props} />
      </QueryClientProvider>,
    );
  });
  await vi.waitFor(() => expect(api.listMachineTokens).toHaveBeenCalled());
}

function findButton(text: string): HTMLButtonElement | undefined {
  return [...document.querySelectorAll("button")].find((item) => item.textContent?.trim() === text);
}

function clickButton(text: string) {
  const button = findButton(text);
  if (!(button instanceof HTMLButtonElement)) throw new Error(`button not found: ${text}`);
  button.click();
}

afterEach(async () => {
  if (root) await act(async () => root?.unmount());
  container?.remove();
  root = undefined;
  container = undefined;
  document.body.innerHTML = "";
  vi.clearAllMocks();
});

describe("Machine token management", () => {
  test("签发成功后只在关闭前展示一次明文", async () => {
    const api = buildApi();
    await renderPage(api, {
      initialIssueOpen: true,
      initialIssueForm: { serviceName: "order", environment: "dev" },
    });

    await vi.waitFor(() => {
      expect(findButton("Issue")?.disabled).toBe(false);
    });
    await act(async () => clickButton("Issue"));

    await vi.waitFor(() => {
      expect(document.querySelector('[data-testid="issued-token"]')?.textContent).toContain("ct_once_only_plaintext");
    });
    expect(document.body.textContent).toContain("This is the only time the plaintext token will be shown");

    await act(async () => clickButton("I have copied it"));
    expect(document.body.textContent).not.toContain("ct_once_only_plaintext");
  });

  test("列表将 service 和 environment 筛选参数传给 RPC", async () => {
    const api = buildApi();
    await renderPage(api, { initialFilters: { serviceName: "gateway", environment: "pre" } });

    await vi.waitFor(() => {
      expect(api.listMachineTokens).toHaveBeenLastCalledWith(
        { serviceName: "gateway", environment: "pre" },
        expect.any(AbortSignal),
      );
    });
  });
});
