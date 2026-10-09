import { test, expect, Page } from "@playwright/test";

test.skip(
  process.env.LIVE_MODEL_TESTS !== "true",
  "Opt-in: uses the live model and changes fictional P018 appointments.",
);
test.setTimeout(240_000);

type RecordItem = {
  id: string;
  name: string;
  start: string;
  status: string;
  related_id: string;
};
async function send(page: Page, message: string) {
  await page.getByLabel("Message", { exact: true }).fill(message);
  await page.getByRole("button", { name: "Send ↗", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Send ↗", exact: true }),
  ).toBeVisible({ timeout: 120_000 });
  await expect(page.getByRole("alert")).toHaveCount(0);
}
async function result(page: Page, tool: string): Promise<RecordItem[]> {
  const entries = await page.locator(".trace details pre").allTextContents();
  for (const text of entries) {
    const data = JSON.parse(text);
    if (data.tool === tool && data.result) {
      expect(data.result.error).toBeUndefined();
      return data.result.records;
    }
  }
  throw new Error(`No successful result for ${tool}`);
}
async function confirm(
  page: Page,
  tool: string,
  id: string,
  acknowledgement: string,
) {
  const proposal = page.locator(".confirmation");
  await expect(proposal).toHaveCount(1);
  await expect(page.locator(".trace")).toContainText(tool);
  await expect(proposal).toContainText(id);
  await proposal.getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Send ↗", exact: true }),
  ).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(proposal).toHaveCount(0);
  await expect(page.locator(".bubble.assistant").last()).toContainText(
    acknowledgement,
  );
  await expect(page.locator(".trace")).toContainText("agent.response.grounded");
}

test("live hospital availability, confirmed booking, rescheduling and cancellation", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Crescent Hospital" }).click();
  await page.getByLabel("Demo identity", { exact: true }).selectOption("P018");
  await page.getByRole("button", { name: /^Go live with/ }).click();
  await expect(page.getByText("Connected", { exact: true })).toBeVisible();
  const tomorrow = new Date();
  tomorrow.setUTCDate(tomorrow.getUTCDate() + 1);
  const date = tomorrow.toISOString().slice(0, 10);
  await send(
    page,
    `Find morning appointments with Dr. Ahmed on ${date}. Do not book yet.`,
  );
  await expect(page.locator(".confirmation")).toHaveCount(0);
  const available = await result(page, "appointment.availability");
  expect(available.length).toBeGreaterThan(1);
  for (const slot of available) {
    expect(slot.related_id).toBe("D001");
    expect(slot.start.slice(0, 10)).toBe(date);
    expect(new Date(slot.start).getUTCHours()).toBeLessThan(12);
  }
  const reply = page.locator(".bubble.assistant").last();
  await expect(reply).toContainText("Available appointments (UTC)");
  const weekday = new Intl.DateTimeFormat("en-US", {
    weekday: "short",
    timeZone: "UTC",
  }).format(tomorrow);
  await expect(reply).toContainText(`${weekday} ${date}`);
  for (const slot of available) await expect(reply).toContainText(slot.id);
  const [first, second] = available;
  await send(page, `Book slot ${first.id}.`);
  await confirm(page, "appointment.book", first.id, "Appointment booked.");
  const [booked] = await result(page, "appointment.book");
  expect(booked.start).toBe(first.start);
  expect(booked.status).toBe("booked");
  await expect(page.locator(".bubble.assistant").last()).toContainText(
    first.start.slice(0, 16).replace("T", " ") + " UTC",
  );

  await send(page, `Reschedule appointment ${booked.id} to slot ${second.id}.`);
  await confirm(
    page,
    "appointment.reschedule",
    second.id,
    "Appointment rescheduled.",
  );
  const [moved] = await result(page, "appointment.reschedule");
  expect(moved.id).toBe(booked.id);
  expect(moved.start).toBe(second.start);

  await send(page, `Cancel appointment ${booked.id}.`);
  await confirm(
    page,
    "appointment.cancel",
    booked.id,
    "Appointment cancelled.",
  );
  const [cancelled] = await result(page, "appointment.cancel");
  expect(cancelled.id).toBe(booked.id);
  expect(cancelled.status).toBe("cancelled");
  await page.screenshot({
    path: "/tmp/enterprise-live-booking.png",
    fullPage: true,
  });
});
