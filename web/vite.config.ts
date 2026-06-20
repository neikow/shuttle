/// <reference types="vitest/config" />
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Control-plane endpoints proxied to a local orchestrator during `make web-dev`
// so the SPA works without CORS. Production serves same-origin (embedded), so
// the proxy is dev-only.
const apiPaths = [
  "/whoami",
  "/overview",
  "/deploys",
  "/audit",
  "/plan",
  "/check",
  "/hosts",
  "/events",
  "/healthz",
  "/deploy",
  "/rollback",
  "/prune",
  "/tokens",
  "/webhooks/repo",
  "/enroll",
];

// Served by the orchestrator under /ui/, so assets must resolve there.
export default defineConfig({
  base: "/ui/",
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    proxy: Object.fromEntries(apiPaths.map((p) => [p, "http://localhost:8080"])),
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
    css: false,
  },
});
