import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: ".",
  testMatch: "*.spec.ts",
  timeout: 15_000,
  use: {
    baseURL: process.env.BASE_URL || "http://localhost:9090",
    browserName: "firefox",
  },
  projects: [{ name: "firefox", use: { browserName: "firefox" } }],
});
