import { expect, test, type Page } from "@playwright/test";
import { migratePolicyRoles } from "../policy-role";

const CUTOVER_ENABLED = process.env.E2E_CUSTOMER_ROLE_CUTOVER === "true";
const POLICY_KEY = "policies/policies.csv";
const EDIT_URL = `/edit?ns=gateway&env=pre&key=${encodeURIComponent(POLICY_KEY)}`;

function versionChip(page: Page) {
  return page.locator(".MuiChip-label").filter({ hasText: /^v\d+$/ });
}

async function editorValue(page: Page): Promise<string> {
  return page.evaluate(() => {
    const monaco = (window as unknown as { monaco?: any }).monaco;
    const model = monaco?.editor?.getModels?.()?.[0];
    if (!model) throw new Error("拿不到 Monaco model");
    return model.getValue() as string;
  });
}

test("gateway/pre 严格切换 Casbin 角色 consumer → customer", async ({ page }) => {
  test.skip(!CUTOVER_ENABLED, "只在显式 customer role cutover 时修改线上策略");

  await page.goto(EDIT_URL);
  await expect(page.getByRole("button", { name: "登出" })).toBeVisible({ timeout: 30_000 });
  await expect(page.locator(".monaco-editor")).toBeVisible({ timeout: 45_000 });

  const current = await editorValue(page);
  const currentVersion = await versionChip(page).textContent();
  const version = Number(currentVersion?.replace(/^v/, ""));
  expect(Number.isInteger(version)).toBe(true);
  const migrated = migratePolicyRoles(current);
  if (migrated.replacements === 0) {
    expect(current).toMatch(/(?:^|,)\s*customer\s*(?:,|$)/m);
    return;
  }

  await page.evaluate((value) => {
    const monaco = (window as unknown as { monaco?: any }).monaco;
    const model = monaco?.editor?.getModels?.()?.[0];
    if (!model) throw new Error("拿不到 Monaco model");
    model.setValue(value);
  }, migrated.value);

  const save = page.getByRole("button", { name: "保存" });
  await expect(save).toBeEnabled({ timeout: 15_000 });
  await save.click();
  await expect(page.locator(".MuiAlert-standardError")).toHaveCount(0, { timeout: 15_000 });
  await expect(versionChip(page)).toHaveText(`v${version + 1}`, { timeout: 30_000 });

  await page.reload();
  await expect(page.locator(".monaco-editor")).toBeVisible({ timeout: 45_000 });
  const saved = await editorValue(page);
  expect(migratePolicyRoles(saved).replacements).toBe(0);
  expect(saved).toMatch(/(?:^|,)\s*customer\s*(?:,|$)/m);
});
