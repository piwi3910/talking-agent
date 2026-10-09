import { test, expect } from "@playwright/test";

test("launcher picks the persona, the stage keeps operator detail in the X-ray", async ({
  page,
}) => {
  await page.goto("/");
  await expect(
    page.getByRole("button", { name: /Nova Telecom/ }),
  ).toHaveAttribute("aria-pressed", "true");
  await page.getByRole("button", { name: "Crescent Hospital" }).click();
  await expect(
    page.getByRole("button", { name: /Crescent Hospital/ }),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByLabel("Demo identity", { exact: true })).toHaveValue(
    "P001",
  );
  await page.getByRole("button", { name: "Web chat" }).click();
  await page.getByRole("button", { name: /^Go live with Maya/ }).click();
  await expect(page.getByText("Connected", { exact: true })).toBeVisible();
  const customer = page.getByRole("region", {
    name: "Customer experience",
    exact: true,
  });
  await expect(customer.getByRole("combobox")).toHaveCount(0);
  await expect(customer.locator(".suggestions")).toHaveCount(0);
  await expect(customer).not.toContainText("memory namespace");
  await expect(page.getByLabel("Message", { exact: true })).toBeEnabled();
  const xray = page.getByRole("complementary", { name: "Under the hood" });
  await expect(xray).toBeVisible();
  await page.getByRole("button", { name: "X-ray" }).click();
  await expect(xray).toBeHidden();
  await page.getByRole("link", { name: "Cockpit" }).click();
  await expect(
    page.getByRole("heading", { name: "Presenter cockpit" }),
  ).toBeVisible();
  await page.getByRole("switch", { name: "X-ray on stage" }).click();
  await page.getByRole("link", { name: "Open the audience stage" }).click();
  await expect(xray).toBeVisible();
  await expect(page.getByText("Connected", { exact: true })).toBeVisible();
  await page.screenshot({
    path: "/tmp/enterprise-stage-hospital.png",
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
    path: "/tmp/enterprise-stage-mobile.png",
    fullPage: true,
  });
  await page.getByRole("button", { name: "End session" }).click();
  await expect(page.getByRole("button", { name: "New session" })).toBeEnabled();
  await page.goto("/settings/personas");
  await expect(
    page.getByRole("region", { name: "Maya capabilities" }),
  ).toContainText("hospital-demo");
});
