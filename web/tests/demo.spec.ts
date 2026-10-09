import { test, expect, Page } from "@playwright/test";
async function send(page: Page, text: string) {
  await page.getByLabel("Message", { exact: true }).fill(text);
  await page.getByRole("button", { name: "Send ↗", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Send ↗", exact: true }),
  ).toBeVisible();
}
test("telecom recurrence, confirmation, and live tool trace", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByRole("button", { name: /^Go live with/ }).click();
  await expect(page.getByText("Connected", { exact: true })).toBeVisible();
  await send(page, "My internet upstairs is terrible again.");
  await expect(page.locator(".bubble.assistant").last()).toContainText(
    "Last time we found channel interference",
  );
  await expect(
    page.locator(".tool-row").filter({ hasText: "wifi.diagnostics" }),
  ).toBeVisible();
  await send(page, "Optimize my Wi-Fi channel");
  await expect(page.locator(".confirmation")).toContainText(
    "Update your Wi-Fi settings?",
  );
  await page.getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(page.locator(".bubble.assistant").last()).toContainText(
    "Interference cleared",
  );
  await expect(
    page.getByRole("button", { name: "Send ↗", exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: "/tmp/enterprise-telecom.png",
    fullPage: true,
  });
});
