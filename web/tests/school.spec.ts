import { test, expect, Page } from "@playwright/test";

test.skip(
  process.env.SCHOOL_LIVE_TESTS !== "true",
  "Opt-in: live school model and fictional F020 tour.",
);
test.setTimeout(240_000);
async function send(page: Page, text: string) {
  await page.getByLabel("Message", { exact: true }).fill(text);
  await page.getByRole("button", { name: "Send ↗", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Send ↗", exact: true }),
  ).toBeVisible({ timeout: 120_000 });
  await expect(page.getByRole("alert")).toHaveCount(0);
}
async function records(page: Page, tool: string) {
  for (const text of await page
    .locator(".trace details pre")
    .allTextContents()) {
    const event = JSON.parse(text);
    if (event.tool === tool && event.result) {
      expect(event.result.error).toBeUndefined();
      return event.result.records;
    }
  }
  throw new Error(`No result from ${tool}`);
}
test("school persona, live tour lookup, confirmed booking and cancellation", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Willowbrook School" }).click();
  await expect(
    page.getByRole("heading", { name: "Demo family" }),
  ).toBeVisible();
  await page.getByLabel("Demo identity", { exact: true }).selectOption("F020");
  await page.getByRole("button", { name: /^Go live with/ }).click();
  await expect(page.getByText("Connected", { exact: true })).toBeVisible();
  await send(
    page,
    "Please find available morning school tours. Do not book yet.",
  );
  const slots = await records(page, "tour.availability");
  expect(slots.length).toBeGreaterThan(0);
  for (const slot of slots)
    expect(Number(slot.start.slice(11, 13))).toBeLessThan(12); // local hour
  await send(page, `Book school tour slot ${slots[0].id}.`);
  let proposal = page.locator(".confirmation");
  await expect(proposal).toHaveCount(1);
  await expect(proposal).toContainText(slots[0].id);
  await proposal.getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(page.locator(".bubble.assistant").last()).toContainText(
    "campus tour is booked",
    { timeout: 20_000 },
  );
  const visits = await records(page, "tour.book");
  expect(visits[0].status).toBe("booked");
  await send(page, `Cancel my school visit ${visits[0].id}.`);
  proposal = page.locator(".confirmation");
  await expect(proposal).toHaveCount(1);
  await proposal.getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(page.locator(".bubble.assistant").last()).toContainText(
    "campus tour is cancelled",
    { timeout: 20_000 },
  );
  await page.screenshot({ path: "/tmp/school-live.png", fullPage: true });
});
