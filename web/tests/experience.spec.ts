import { test, expect } from "@playwright/test";

test("branded chat keeps operator controls in the playground and has no suggested questions", async ({
  page,
}) => {
  await page.goto("/");
  const customer = page.getByRole("region", {
    name: "Customer experience",
    exact: true,
  });
  await expect(customer).toContainText("Nova Telecom");
  await expect(customer.getByRole("combobox")).toHaveCount(0);
  await expect(customer.locator(".suggestions")).toHaveCount(0);
  await expect(customer).not.toContainText("memory namespace");
  await page.getByRole("button", { name: "Willowbrook School" }).click();
  await expect(customer).toContainText("Emma");
  await expect(customer).toContainText("Willowbrook School");
  await expect(page.getByLabel("Demo identity", { exact: true })).toHaveValue(
    "F001",
  );
  await page.getByRole("tab", { name: "capabilities", exact: true }).click();
  await expect(
    page.getByRole("tabpanel", { name: "capabilities", exact: true }),
  ).toContainText("school-demo");
  await page.getByRole("button", { name: "Hide playground" }).click();
  await expect(
    page.getByRole("complementary", { name: "Agent playground" }),
  ).toBeHidden();
  await page.getByRole("button", { name: "Start chat" }).click();
  await expect(page.getByText("Connected", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Message", { exact: true })).toBeEnabled();
  await page.getByRole("button", { name: "Open playground" }).click();
  await page.getByRole("tab", { name: "personas", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "New session", exact: true }),
  ).toBeEnabled();
  await page.screenshot({
    path: "/tmp/enterprise-phase2-school.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await expect(page.getByLabel("Message", { exact: true })).toBeVisible();
  await page.screenshot({
    path: "/tmp/enterprise-phase2-mobile.png",
    fullPage: true,
  });
});
