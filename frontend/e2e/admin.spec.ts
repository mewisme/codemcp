import { expect, test } from "@playwright/test"

import {
  expectBothAxisOverflow,
  expectHorizontalOverflow,
  expectNoViewportOverflow,
  expectVerticalOverflow,
  installAdminMocks,
  tabTo,
} from "./fixtures"

test.describe("Browser Admin interaction quality", () => {
  test("shell keeps grouped navigation, one page title, and mobile-safe chrome", async ({
    page,
  }, testInfo) => {
    await installAdminMocks(page)
    await page.goto("/overview")

    if (testInfo.project.name === "admin-mobile") {
      await page.getByRole("button", { name: "Toggle Sidebar" }).click()
    }

    for (const label of ["Operations", "Projects", "Runtime", "Connections"]) {
      await expect(
        page.locator('[data-sidebar="group-label"]').filter({ hasText: label })
      ).toBeVisible()
    }
    await expect(
      page.getByRole("button", { name: "Change theme" })
    ).toBeVisible()
    await page.getByRole("link", { name: "Tools" }).click()
    await expect(page).toHaveURL(/\/tools$/)
    await expect(
      page.getByRole("heading", { level: 1, name: "Tools" })
    ).toHaveCount(1)
    await expect(page.getByLabel("Admin shell")).not.toContainText("Tools")
    await expectNoViewportOverflow(page)

    if (testInfo.project.name === "admin-mobile") {
      await expect(page.getByRole("link", { name: "Tools" })).toBeHidden()
    } else {
      await expect(page.getByRole("link", { name: "Tools" })).toHaveAttribute(
        "data-active",
        "true"
      )
    }
  })

  test("logs stay reachable, scrollable, and distinguish view clear from journal deletion", async ({
    page,
  }) => {
    await installAdminMocks(page)
    await page.goto("/logs")

    await expect(page.getByRole("heading", { name: "Logs" })).toBeVisible()
    await expectNoViewportOverflow(page)

    const workspace = page.getByLabel("Workspace filter")
    const search = page.getByLabel("Search logs")
    await tabTo(page, workspace)
    await workspace.fill("ws_fixture")
    await tabTo(page, search)
    await search.fill("payload")
    await page.getByRole("button", { name: "Apply" }).click()

    const eventScroll = page.locator('[data-slot="scroll-area"]').first()
    await expectBothAxisOverflow(eventScroll)

    await page.getByRole("button", { name: "Clear view" }).click()
    await expect(page.getByText("runtime.long_payload")).toHaveCount(0)
    await page.getByRole("button", { name: "Refresh" }).click()
    await expect(
      page.getByText("runtime.long_payload", { exact: true })
    ).toBeVisible()

    const deleteJournal = page.getByRole("button", { name: "Delete journal" })
    await tabTo(page, deleteJournal)
    await page.keyboard.press("Enter")

    const dialog = page.getByRole("alertdialog")
    await expect(dialog).toBeVisible()
    await expect(
      dialog.getByRole("button", { name: "Delete logs permanently" })
    ).toBeVisible()
    const box = await dialog.boundingBox()
    const viewport = page.viewportSize()
    expect(box).not.toBeNull()
    expect(viewport).not.toBeNull()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.y).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(viewport!.width + 1)
    expect(box!.y + box!.height).toBeLessThanOrEqual(viewport!.height + 1)

    await dialog
      .getByRole("button", { name: "Delete logs permanently" })
      .focus()
    await page.keyboard.press("Enter")
    await expect(dialog).toHaveCount(0)
  })

  test("long tool detail remains inside the viewport with two-axis scrolling and keyboard dismissal", async ({
    page,
  }, testInfo) => {
    await installAdminMocks(page)
    await page.goto("/tools")

    await expect(page.getByText("quality_fixture_tool").first()).toBeVisible()
    if (testInfo.project.name !== "admin-mobile") {
      await expect(page.getByText("Page 1 of 2 · 25 rows")).toBeVisible()
      const next = page.getByRole("button", { name: "Next page" })
      await tabTo(page, next)
      await page.keyboard.press("Enter")
      await expect(page.getByText("Page 2 of 2 · 25 rows")).toBeVisible()
      await page.getByRole("button", { name: "Previous page" }).click()
      await expect(page.getByText("Page 1 of 2 · 25 rows")).toBeVisible()
    }

    await page.getByText("quality_fixture_tool").first().click()

    const dialog = page.getByRole("dialog")
    await expect(dialog).toBeVisible()
    await dialog.getByRole("tab", { name: "Input schema" }).click()

    const scrollAreas = dialog.locator('[data-slot="scroll-area"]')
    await expectHorizontalOverflow(
      testInfo.project.name === "admin-mobile"
        ? scrollAreas.last()
        : scrollAreas.first()
    )
    await expectVerticalOverflow(
      scrollAreas.last().locator(':scope > [data-slot="scroll-area-viewport"]')
    )
    await expectNoViewportOverflow(page)

    const box = await dialog.boundingBox()
    const viewport = page.viewportSize()
    expect(box).not.toBeNull()
    expect(viewport).not.toBeNull()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.y).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(viewport!.width + 1)
    expect(box!.y + box!.height).toBeLessThanOrEqual(viewport!.height + 1)

    if (testInfo.project.name === "admin-desktop") {
      await expect(dialog).toHaveScreenshot("tool-detail.png", {
        animations: "disabled",
      })
    }

    await page.keyboard.press("Escape")
    await expect(dialog).toHaveCount(0)
  })

  test("stream failure recovers without leaving navigation blocked by loading state", async ({
    page,
  }) => {
    const stream = await installAdminMocks(page, {
      activityReconnect: true,
    })
    await page.goto("/activity")

    await expect(page.getByText("Connecting")).toBeVisible()
    await expect.poll(stream.activityCalls).toBeGreaterThanOrEqual(2)
    await expect(page.getByText("Live", { exact: true })).toBeVisible({
      timeout: 4_000,
    })

    const search = page.getByPlaceholder(
      "Search tool, workspace, source, message..."
    )
    await tabTo(page, search)
    await search.fill("tool")
    await expectNoViewportOverflow(page)
  })
})
