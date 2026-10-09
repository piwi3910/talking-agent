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
test("hospital preferences survive new sessions and appointments book", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Crescent Hospital" }).click();
  await page.getByLabel("Demo identity", { exact: true }).selectOption("P006");
  await page.getByRole("button", { name: /^Go live with/ }).click();
  await send(page, "Mornings normally work better for me.");
  await expect(page.locator(".trace")).toContainText("memory.store.completed");
  await page.getByRole("button", { name: "End session" }).click();
  await page.getByRole("button", { name: "New session" }).click();
  await send(page, "Can I make another appointment with Dr. Ahmed?");
  const reply = page.locator(".bubble.assistant").last();
  await expect(page.locator(".memory")).toContainText(
    "User generally prefers morning appointments.",
  );
  await expect(reply).toContainText("Available appointments (UTC)");
  const text = await reply.innerText();
  const slot = text.match(/S-D\d{3}-\d{8}-\d{2}/)?.[0];
  expect(slot).toBeTruthy();
  await send(page, `Book ${slot}`);
  await expect(page.locator(".confirmation")).toContainText(
    "Book this appointment?",
  );
  await page.getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(page.locator(".bubble.assistant").last()).toContainText(
    "Appointment booked",
  );
  await expect(
    page.getByRole("button", { name: "Send ↗", exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: "/tmp/enterprise-hospital.png",
    fullPage: true,
  });
});
test("hospital urgent safety path bypasses tools", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Crescent Hospital" }).click();
  await page.getByRole("button", { name: /^Go live with/ }).click();
  await send(page, "I have chest pain and can't breathe. Book an appointment.");
  await expect(page.locator(".bubble.assistant").last()).toContainText(
    "emergency services now",
  );
  await expect(page.locator(".tool-row")).toHaveCount(0);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByLabel("Message", { exact: true })).toBeVisible();
});
