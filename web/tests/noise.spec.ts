import { test, expect } from "@playwright/test";
test.skip(
  process.env.VOICE_NOISE_TESTS !== "true",
  "Requires the deterministic noise/speech WAV fixture",
);
test.use({
  permissions: ["microphone"],
  launchOptions: {
    args: [
      "--use-fake-ui-for-media-stream",
      "--use-fake-device-for-media-stream",
      "--use-file-for-fake-audio-capture=/tmp/enterprise-voice-noise.wav",
    ],
  },
});
test("non-speech hum and clicks do not start turns; subsequent speech does", async ({
  page,
}) => {
  test.setTimeout(45000);
  let captures = 0;
  page.on("websocket", (s) => {
    if (s.url().includes("/transcribe")) captures++;
  });
  await page.goto("/");
  await page.getByRole("button", { name: /^Go live with/ }).click();
  await page.getByRole("button", { name: "Start voice", exact: true }).click();
  await page.waitForTimeout(6500);
  expect(captures).toBe(0);
  await expect(page.locator(".bubble.user")).toHaveCount(0);
  await expect(page.locator(".bubble.user")).toContainText("Tuesday", {
    timeout: 25000,
  });
  expect(captures).toBe(1);
  await page.getByRole("button", { name: "End voice", exact: true }).click();
  await expect(page.getByRole("alert")).toHaveCount(0);
});
