import { defineConfig } from "@playwright/test"

const apiPort = 18091
const webPort = 13100
const webCommand = process.env.CI
  ? "node e2e/serve-built.mjs"
  : `pnpm exec next dev --hostname 127.0.0.1 --port ${webPort}`

export default defineConfig({
  testDir: "./e2e",
  testMatch: "**/*.e2e.ts",
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? "github" : "list",
  use: {
    baseURL: `http://127.0.0.1:${webPort}`,
    browserName: "chromium",
    trace: "retain-on-failure",
    viewport: { width: 1440, height: 900 },
  },
  webServer: [
    {
      command: `node e2e/mock-api.mjs ${apiPort}`,
      port: apiPort,
      reuseExistingServer: false,
    },
    {
      command: webCommand,
      env: {
        AGENTBOX_API_URL: `http://127.0.0.1:${apiPort}`,
        HOSTNAME: "127.0.0.1",
        NEXT_TELEMETRY_DISABLED: "1",
        PORT: String(webPort),
      },
      port: webPort,
      reuseExistingServer: false,
      timeout: 120_000,
    },
  ],
})
