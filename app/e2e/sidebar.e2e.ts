import { expect, test } from "@playwright/test"

const apiOrigin = "http://127.0.0.1:18091"

test.beforeEach(async ({ request }) => {
  const response = await request.post(`${apiOrigin}/__e2e/reset`)
  expect(response.ok()).toBe(true)
})

test("resizes the desktop sidebar with pointer and keyboard input", async ({
  page,
}) => {
  await page.goto("/overview")

  const resizeHandle = page.getByRole("separator", {
    name: "调整侧栏宽度",
  })
  const sidebar = page.locator('[data-slot="sidebar-container"]')
  const main = page.locator("#main-content")

  await expect(resizeHandle).toHaveAttribute("aria-orientation", "vertical")
  await expect(resizeHandle).toHaveAttribute("aria-controls", "app-sidebar")
  await expect(resizeHandle).toHaveAttribute("aria-valuemin", "224")
  await expect(resizeHandle).toHaveAttribute("aria-valuemax", "384")
  await expect(resizeHandle).toHaveAttribute("aria-valuenow", "256")

  const handleBox = await resizeHandle.boundingBox()
  const mainBox = await main.boundingBox()
  expect(handleBox).not.toBeNull()
  expect(mainBox).not.toBeNull()

  await page.mouse.move(
    handleBox!.x + handleBox!.width / 2,
    handleBox!.y + handleBox!.height / 2
  )
  await page.mouse.down()
  await page.mouse.move(
    handleBox!.x + handleBox!.width / 2 + 80,
    handleBox!.y + handleBox!.height / 2,
    { steps: 4 }
  )
  await page.mouse.up()

  await expect(resizeHandle).toHaveAttribute("aria-valuenow", "336")
  await expect(sidebar).toHaveCSS("width", "336px")
  await expect
    .poll(async () => Math.round((await main.boundingBox())!.x - mainBox!.x))
    .toBe(80)

  await resizeHandle.focus()
  await resizeHandle.press("ArrowLeft")
  await expect(resizeHandle).toHaveAttribute("aria-valuenow", "320")

  await resizeHandle.press("Home")
  await expect(resizeHandle).toHaveAttribute("aria-valuenow", "224")
  await expect(sidebar).toHaveCSS("width", "224px")

  await resizeHandle.press("End")
  await expect(resizeHandle).toHaveAttribute("aria-valuenow", "384")
  await expect(sidebar).toHaveCSS("width", "384px")
})

test("keeps the resize handle out of the mobile sidebar", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/overview")

  await expect(
    page.getByRole("separator", { name: "调整侧栏宽度" })
  ).toHaveCount(0)

  await page.getByRole("button", { name: "Toggle Sidebar" }).click()
  const mobileSidebar = page.getByRole("dialog", { name: "Sidebar" })
  await expect(mobileSidebar).toBeVisible()
  await expect(
    mobileSidebar.getByRole("separator", { name: "调整侧栏宽度" })
  ).toHaveCount(0)

  await mobileSidebar.getByRole("link", { name: "项目", exact: true }).click()
  await expect(page).toHaveURL(/\/projects$/)
  await expect(mobileSidebar).toBeHidden()
})
