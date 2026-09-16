import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { afterEach, beforeAll, describe, expect, test, vi } from "vite-plus/test";
import { TokensPage } from "@/components/TokensPage";
import {
  IssueMachineTokenResponseSchema,
  ListMachineTokensResponseSchema,
  RevokeMachineTokenResponseSchema,
  MachineTokenRole,
} from "@/gen/api";
import { forgetAllIssuedTokens } from "@/store/issued-tokens";
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

const TOKEN_ID = "6f1c6d0e-6b4a-4a1f-9a2e-2f5c7d8e9a01";

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

// 多个 MUI Dialog 会同时留在 DOM 里（退出动画期间），按标题定位到目标弹窗再点按钮。
function findDialog(title: string): HTMLElement | undefined {
  return [...document.querySelectorAll<HTMLElement>('[role="dialog"]')].find(
    (item) => item.textContent?.includes(title),
  );
}

function clickInDialog(title: string, text: string) {
  const dialog = findDialog(title);
  if (!dialog) throw new Error(`dialog not found: ${title}`);
  const button = [...dialog.querySelectorAll("button")].find((item) => item.textContent?.trim() === text);
  if (!(button instanceof HTMLButtonElement)) throw new Error(`button not found: ${title} / ${text}`);
  button.click();
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
  forgetAllIssuedTokens();
  vi.clearAllMocks();
});

describe("Machine token management", () => {
  test("关闭签发弹窗需要二次确认才丢弃明文", async () => {
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

    // 第一次点关闭只弹确认框，明文还在。
    await act(async () => clickButton("I have copied it"));
    expect(document.body.textContent).toContain("Close and discard?");
    expect(document.body.textContent).toContain("ct_once_only_plaintext");

    // 取消后回到明文视图。
    await act(async () => clickInDialog("Close and discard?", "Cancel"));
    await vi.waitFor(() => expect(findDialog("Close and discard?")).toBe(undefined));
    expect(document.querySelector('[data-testid="issued-token"]')?.textContent).toContain("ct_once_only_plaintext");

    await act(async () => clickButton("I have copied it"));
    await act(async () => clickButton("Close and discard"));
    expect(document.body.textContent).not.toContain("ct_once_only_plaintext");
  });

  test("收起弹窗后仍可从列表重新查看明文", async () => {
    const api = buildApi();
    api.issueMachineToken.mockResolvedValue(
      create(IssueMachineTokenResponseSchema, {
        token: "ct_once_only_plaintext",
        meta: { id: TOKEN_ID, serviceName: "order", environment: "dev" },
      }),
    );
    api.listMachineTokens.mockResolvedValue(
      create(ListMachineTokensResponseSchema, {
        tokens: [{ id: TOKEN_ID, serviceName: "order", environment: "dev" }],
      }),
    );
    await renderPage(api, {
      initialIssueOpen: true,
      initialIssueForm: { serviceName: "order", environment: "dev" },
    });

    await vi.waitFor(() => expect(findButton("Issue")?.disabled).toBe(false));
    await act(async () => clickButton("Issue"));
    await vi.waitFor(() => {
      expect(document.querySelector('[data-testid="issued-token"]')?.textContent).toContain("ct_once_only_plaintext");
    });

    // 「稍后再看」只收起视图，明文留在内存里。
    await act(async () => clickButton("View later"));
    expect(document.querySelector('[data-testid="issued-token"]')?.textContent ?? "").toBe("");

    await vi.waitFor(() => expect(findButton("View plaintext")).toBeTruthy());
    await act(async () => clickButton("View plaintext"));
    expect(document.querySelector('[data-testid="issued-token"]')?.textContent).toContain("ct_once_only_plaintext");
  });

  test("operator 角色随签发请求发送", async () => {
    const api = buildApi();
    await renderPage(api, {
      initialIssueOpen: true,
      initialIssueForm: { serviceName: "harvest", environment: "pre", role: MachineTokenRole.OPERATOR },
    });
    await vi.waitFor(() => expect(findButton("Issue")?.disabled).toBe(false));
    await act(async () => clickButton("Issue"));
    await vi.waitFor(() => expect(api.issueMachineToken).toHaveBeenCalledWith(
      expect.objectContaining({ serviceName: "harvest", environment: "pre", role: MachineTokenRole.OPERATOR }),
    ));
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
