import { defineConfig } from "@playwright/test"

const adminURL = "http://127.0.0.1:4473"
const miniAppURL = "http://127.0.0.1:4474/mini-app.html"

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  expect: { timeout: 7_500 },
  fullyParallel: false,
  workers: process.env.CI ? 2 : undefined,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    colorScheme: "light",
    locale: "en-US",
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
    video: "off",
  },
  snapshotPathTemplate:
    "{testDir}/__screenshots__/{testFilePath}/{arg}-{projectName}{ext}",
  webServer: [
    {
      command:
        "./node_modules/.bin/vite --force --host 127.0.0.1 --port 4473 --strictPort",
      url: adminURL,
      reuseExistingServer: !process.env.CI,
      timeout: 60_000,
    },
    {
      command:
        "./node_modules/.bin/vite --force --config vite.mini-app.config.ts --host 127.0.0.1 --port 4474 --strictPort",
      url: miniAppURL,
      reuseExistingServer: !process.env.CI,
      timeout: 60_000,
    },
  ],
  projects: [
    {
      name: "admin-desktop",
      testMatch: /admin\.spec\.ts/,
      use: { baseURL: adminURL, viewport: { width: 1440, height: 900 } },
    },
    {
      name: "admin-tablet",
      testMatch: /admin\.spec\.ts/,
      use: { baseURL: adminURL, viewport: { width: 820, height: 1180 } },
    },
    {
      name: "admin-mobile",
      testMatch: /admin\.spec\.ts/,
      use: {
        baseURL: adminURL,
        viewport: { width: 390, height: 844 },
        isMobile: true,
        hasTouch: true,
      },
    },
    {
      name: "mini-mobile",
      testMatch: /mini-app\.spec\.ts/,
      use: {
        baseURL: miniAppURL,
        viewport: { width: 390, height: 844 },
        isMobile: true,
        hasTouch: true,
      },
    },
    {
      name: "mini-desktop",
      testMatch: /mini-app\.spec\.ts/,
      use: { baseURL: miniAppURL, viewport: { width: 1024, height: 768 } },
    },
  ],
})
