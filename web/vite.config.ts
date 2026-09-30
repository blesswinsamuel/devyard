/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import solid from "vite-plugin-solid";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";

const backend = process.env.DEVYARD_WEB_BACKEND ?? "http://127.0.0.1:9090";

// DEVYARD_WEB_ALLOWED_HOSTS: comma-separated hostnames (leading "." matches
// subdomains) the dev server accepts. Unset allows any host.
const allowedHosts = process.env.DEVYARD_WEB_ALLOWED_HOSTS?.split(",")
  .map((h) => h.trim())
  .filter(Boolean);

export default defineConfig({
  plugins: [solid(), tailwindcss()],
  resolve: {
    alias: {
      "~": fileURLToPath(new URL("./src", import.meta.url)),
      "lucide-solid": fileURLToPath(
        new URL("./node_modules/lucide-solid/dist/esm/lucide-solid.mjs", import.meta.url),
      ),
    },
  },
  optimizeDeps: {
    include: ["lucide-solid"],
  },
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: false,
  },
  server: {
    host: "0.0.0.0",
    port: 19095,
    strictPort: true,
    allowedHosts: allowedHosts?.length ? allowedHosts : true,
    proxy: {
      "/devyard.v1.DaemonService": { target: backend },
      "/ws": { target: backend, ws: true },
    },
  },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    include: ["src/**/*.test.{ts,tsx}"],
    server: { deps: { inline: [/solid-js/, /@solidjs/, /@kobalte/, /@corvu/] } },
  },
});
