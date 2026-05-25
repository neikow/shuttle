import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// Served by the orchestrator under /ui/, so assets must resolve there.
export default defineConfig({
  base: "/ui/",
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    // Dev: proxy API calls to a local orchestrator so the SPA works without CORS.
    proxy: {
      "/overview": "http://localhost:8080",
      "/deploys": "http://localhost:8080",
      "/plan": "http://localhost:8080",
      "/check": "http://localhost:8080",
      "/hosts": "http://localhost:8080",
      "/events": "http://localhost:8080",
      "/healthz": "http://localhost:8080",
    },
  },
});
