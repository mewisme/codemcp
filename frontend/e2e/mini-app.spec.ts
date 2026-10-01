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
  }) => {
    await installMiniAppMocks(page)
    await page.goto("/mini-app.html")

    await expect(page.getByText("Live")).toBeVisible()
    await expect(page.getByRole("tab", { name: "Runtime" })).toBeVisible()
    await expect(
      page.getByRole("tab", { name: "Command Execute" })
    ).toBeVisible()
    await expect(page.getByRole("tab", { name: "Tool Call/MCP" })).toBeVisible()
    await expectNoViewportOverflow(page)

    const search = page.getByPlaceholder("Search event, component, message…")
    await tabTo(page, search)
    await search.fill("Failure fixture")
    await expect(page.getByText("Failure fixture")).toBeVisible()
    await expect(page.getByText("29 visible")).toHaveCount(0)

    await search.fill("")
    const stateFilter = page.getByRole("combobox").first()
    await tabTo(page, stateFilter)
    await stateFilter.press("Enter")
    await page.getByRole("option", { name: "Errors" }).click()
    await expect(page.getByText("1 visible")).toBeVisible()
    await expect(page.getByText("Failure fixture")).toBeVisible()

    await page.evaluate(() => {
      ;(
        window as typeof window & { __closeMiniSocket?: () => void }
      ).__closeMiniSocket?.()
    })
    await expect(page.getByText("Reconnecting")).toBeVisible()
    await expect(page.getByText("Live")).toBeVisible({ timeout: 4_000 })
    await expectNoViewportOverflow(page)
  })

  test("tool-call detail keeps request and response independently scrollable without trapping the page", async ({
    page,
  }, testInfo) => {
    await installMiniAppMocks(page)
    await page.goto("/mini-app.html")
    await expect(page.getByText("Live")).toBeVisible()

    await page.getByRole("tab", { name: "Tool Call/MCP" }).click()
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
      await page.evaluate(() => {
        ;(
          window as typeof window & { __telegramBack?: () => void }
        ).__telegramBack?.()
      })
      await expect(detail).toHaveCount(0)
    } else {
      await detail.getByRole("button", { name: "Close detail" }).click()
      await expect(detail).toHaveCount(0)
    }
  })

  test("execution response remains vertically reachable at both representative viewports", async ({
    page,
  }, testInfo) => {
    await installMiniAppMocks(page)
    await page.goto("/mini-app.html")
    await expect(page.getByText("Live")).toBeVisible()

    await page.getByRole("tab", { name: "Command Execute" }).click()
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
