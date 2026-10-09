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
  await page.getByRole("button", { name: "Start session" }).click();
  await expect(page.getByText("Connected", { exact: true })).toBeVisible();
  await page.getByRole("tab", { name: "activity", exact: true }).click();
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
test("telecom preferences survive new sessions", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Demo identity", { exact: true }).selectOption("C006");
  await page.getByRole("button", { name: "Start session" }).click();
  await page.getByRole("tab", { name: "activity", exact: true }).click();
  await send(page, "I prefer troubleshooting before you send a technician.");
  await expect(page.locator(".trace")).toContainText("memory.store.completed");
  await page.getByRole("tab", { name: "personas", exact: true }).click();
  await page.getByRole("button", { name: "New session" }).click();
  await page.getByRole("tab", { name: "activity", exact: true }).click();
  await send(page, "My Wi-Fi is down again; can you send a technician?");
  await expect(page.locator(".memory")).toContainText(
    "Customer prefers troubleshooting before technician dispatch.",
  );
  await expect(
    page.getByRole("button", { name: "Send ↗", exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: "/tmp/enterprise-telecom-memory.png",
    fullPage: true,
  });
});
test("urgent safety path bypasses tools", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: /Noor/ }).click();
  await page.getByRole("button", { name: "Start session" }).click();
  await send(
    page,
    "Someone is in immediate danger and can't breathe. Book a tour.",
  );
  await expect(page.locator(".bubble.assistant").last()).toContainText(
    "emergency services",
  );
  await expect(
    page.locator(".tool-row").filter({ hasText: "tour.book" }),
  ).toHaveCount(0);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByLabel("Message", { exact: true })).toBeVisible();
});
