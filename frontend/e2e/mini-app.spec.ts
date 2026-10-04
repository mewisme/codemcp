import { expect, test } from "@playwright/test"

import {
  expectHorizontalOverflow,
  expectNoViewportOverflow,
  expectVerticalOverflow,
  installMiniAppMocks,
  tabTo,
} from "./fixtures"

test.describe("Telegram Mini App interaction quality", () => {
  test("runtime feed stays responsive, keyboard reachable, filterable, and reconnects", async ({
    page,
  }, testInfo) => {
    await installMiniAppMocks(page)
    await page.goto("/mini-app.html")

    await expect(page.getByRole("heading", { name: "Activity" })).toBeVisible()
    await expect(page.getByText("Live", { exact: true })).toBeVisible()
    await expect(page.getByRole("tab", { name: "Runtime" })).toBeVisible()
    await expect(page.getByRole("tab", { name: "Commands" })).toBeVisible()
    await expect(page.getByRole("tab", { name: "Tool calls" })).toBeVisible()
    await expect(page.getByRole("button", { name: "Pause live" })).toHaveCount(
      0
    )
    await expect(page.getByRole("button", { name: "Clear view" })).toHaveCount(
      0
    )
    await expect(page.getByRole("button", { name: "Settings" })).toHaveCount(0)
    await expectNoViewportOverflow(page)

    const search = page.getByPlaceholder("Search event, component, message…")
    await tabTo(page, search)
    await search.fill("Failure fixture")
    await expect(page.getByText("Failure fixture")).toBeVisible()
    await expect(page.getByText("29 visible")).toHaveCount(0)

    await search.fill("")
    if (testInfo.project.name === "mini-mobile") {
      const filters = page.getByRole("button", { name: /^Filters/ })
      await expect(filters).toHaveAttribute("aria-expanded", "false")
      await filters.click()
      await expect(filters).toHaveAttribute("aria-expanded", "true")
    }
    const stateFilter = page.getByLabel("Activity state")
    await tabTo(page, stateFilter)
    await stateFilter.press("Enter")
    await page.getByRole("option", { name: "Errors" }).click()
    await expect(page.getByText("1 visible")).toBeVisible()
    await expect(page.getByText("Failure fixture")).toBeVisible()
    await search.fill("definitely-no-match")
    await expect(
      page.getByText("No activity matches these filters")
    ).toBeVisible()
    await search.fill("")
    if (testInfo.project.name === "mini-mobile") {
      await page.getByRole("button", { name: /^Filters/ }).click()
      await expect(page.getByText("Errors")).toBeVisible()
      await expectNoViewportOverflow(page)
    }

    await page.getByRole("button", { name: "Reset" }).click()
    await expect(page.getByText("30 visible")).toBeVisible()
    await page.getByRole("tab", { name: "Commands" }).click()
    await expect(
      page.getByRole("button", { name: /printf fixture/ })
    ).toBeVisible()
    await expect(page.getByText("Live", { exact: true })).toBeVisible()
    await page.getByRole("tab", { name: "Tool calls" }).click()
    await expect(
      page.getByRole("button", { name: /quality_fixture_tool/ })
    ).toBeVisible()
    await expect(page.getByText("Live", { exact: true })).toBeVisible()
    await page.getByRole("tab", { name: "Runtime" }).click()
    await expect(page.getByText("30 visible")).toBeVisible()

    await page.evaluate(() => {
      ;(
        window as typeof window & { __closeMiniSocket?: () => void }
      ).__closeMiniSocket?.()
    })
    await expect(page.getByText("Reconnecting")).toBeVisible()
    await expect(page.getByText("Live", { exact: true })).toBeVisible({
      timeout: 4_000,
    })
    await expectNoViewportOverflow(page)

    await page.evaluate(() => {
      ;(
        window as typeof window & { __telegramMain?: () => void }
      ).__telegramMain?.()
    })
    await expect(page.getByText("Paused")).toBeVisible()
    await page.evaluate(() => {
      ;(
        window as typeof window & { __telegramMain?: () => void }
      ).__telegramMain?.()
    })
    await expect(page.getByText("Live", { exact: true })).toBeVisible()

    await page.evaluate(() => {
      ;(
        window as typeof window & { __telegramSettings?: () => void }
      ).__telegramSettings?.()
    })
    const settings = page.getByRole("dialog")
    await expect(settings.getByText("View settings")).toBeVisible()
    await expect(settings.getByRole("button", { name: "Close" })).toHaveCount(0)
    await page.evaluate(() => {
      ;(
        window as typeof window & { __telegramBack?: () => void }
      ).__telegramBack?.()
    })
    await expect(settings).toHaveCount(0)

    await page.evaluate(() => {
      ;(
        window as typeof window & { __telegramSecondary?: () => void }
      ).__telegramSecondary?.()
    })
    await expect(page.getByText("View cleared")).toBeVisible()

    await page.evaluate(() => {
      ;(
        window as typeof window & { __telegramDeactivate?: () => void }
      ).__telegramDeactivate?.()
    })
    await expect(page.getByText("Inactive")).toBeVisible()
    await expect(page.getByText("Activity suspended")).toBeVisible()
    await page.evaluate(() => {
      ;(
        window as typeof window & { __telegramActivate?: () => void }
      ).__telegramActivate?.()
    })
    await expect(page.getByText("Live", { exact: true })).toBeVisible({
      timeout: 4_000,
    })
    await expect(page.getByText("View cleared")).toBeVisible()
    await expectNoViewportOverflow(page)
  })

  test("tool-call detail keeps request and response independently scrollable without trapping the page", async ({
    page,
  }, testInfo) => {
    await installMiniAppMocks(page)
    await page.goto("/mini-app.html")
    await expect(page.getByText("Live", { exact: true })).toBeVisible()

    await page.getByRole("tab", { name: "Tool calls" }).click()
    const tool = page.getByRole("button", { name: /quality_fixture_tool/ })
    await expect(tool).toBeVisible()
    await tool.focus()
    await page.keyboard.press("Enter")

    const detail =
      testInfo.project.name === "mini-mobile"
        ? page.locator("[data-mini-app-detail-page]")
        : page.locator("aside")
    await expect(detail).toBeVisible()

    const requestTab = detail.getByRole("tab", { name: "Request" })
    await requestTab.focus()
    await page.keyboard.press("Enter")
    const requestArea = detail
      .getByRole("tabpanel", { name: "Request" })
      .locator(':scope > [data-slot="scroll-area"]')
    await expectHorizontalOverflow(requestArea)

    await detail.getByRole("tab", { name: "Response" }).click()
    const responsePanel = detail.getByRole("tabpanel", { name: "Response" })
    const responseArea = responsePanel.locator(
      ':scope > [data-slot="scroll-area"]'
    )
    await expectHorizontalOverflow(responseArea)
    await expectVerticalOverflow(
      responseArea.locator('[data-slot="scroll-area-viewport"]').first()
    )
    await expectNoViewportOverflow(page)

    const box = await detail.boundingBox()
    const viewport = page.viewportSize()
    expect(box).not.toBeNull()
    expect(viewport).not.toBeNull()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.y).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(viewport!.width + 1)
    expect(box!.y + box!.height).toBeLessThanOrEqual(viewport!.height + 1)

    if (testInfo.project.name === "mini-mobile") {
      await expect(detail).toHaveScreenshot("tool-call-detail.png", {
        animations: "disabled",
      })
    }
    await expect(
      detail.getByRole("button", { name: "Close detail" })
    ).toHaveCount(0)
    await page.evaluate(() => {
      ;(
        window as typeof window & { __telegramBack?: () => void }
      ).__telegramBack?.()
    })
    await expect(detail).toHaveCount(0)
  })

  test("execution response remains vertically reachable at both representative viewports", async ({
    page,
  }, testInfo) => {
    await installMiniAppMocks(page)
    await page.goto("/mini-app.html")
    await expect(page.getByText("Live", { exact: true })).toBeVisible()

    await page.getByRole("tab", { name: "Commands" }).click()
    const execution = page.getByRole("button", { name: /printf fixture/ })
    await expect(execution).toBeVisible()
    await execution.click()

    const detail =
      testInfo.project.name === "mini-mobile"
        ? page.locator("[data-mini-app-detail-page]")
        : page.locator("aside")
    await expect(detail).toBeVisible()
    await detail.getByRole("tab", { name: "Response" }).click()

    const responseArea = detail
      .getByRole("tabpanel", { name: "Response" })
      .locator(':scope > [data-slot="scroll-area"]')
    const viewport = responseArea
      .locator('[data-slot="scroll-area-viewport"]')
      .first()
    await expect(viewport).toBeVisible()
    await expect
      .poll(() =>
        viewport.evaluate(
          (element) => element.scrollHeight > element.clientHeight + 1
        )
      )
      .toBe(true)
    await viewport.evaluate((element) => {
      element.scrollTop = element.scrollHeight
    })
    await expect
      .poll(() => viewport.evaluate((element) => element.scrollTop > 0))
      .toBe(true)
    await expectNoViewportOverflow(page)
  })
})
