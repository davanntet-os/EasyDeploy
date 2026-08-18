import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// During development the React app runs on Vite's dev server and proxies API
// and WebSocket traffic to the Go server. The target defaults to :8080 but
// can be overridden with EASYDEPLOY_API_TARGET when the server runs on a
// different port. In production the Go server serves the built assets.
const apiTarget = process.env.EASYDEPLOY_API_TARGET || "http://localhost:8080";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: apiTarget,
        changeOrigin: true,
        ws: true,
      },
    },
  },
  build: {
    outDir: "dist",
  },
});
