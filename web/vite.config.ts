import { defineConfig } from "vite";
import solid from "vite-plugin-solid";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";

export default defineConfig({
  plugins: [solid(), tailwindcss()],
  resolve: {
    alias: {
      "~": fileURLToPath(new URL("./src", import.meta.url)),
      "lucide-solid": fileURLToPath(
        new URL(
          "./node_modules/lucide-solid/dist/esm/lucide-solid.mjs",
          import.meta.url,
        ),
      ),
    },
  },
  optimizeDeps: {
    include: ["lucide-solid"],
  },
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: true,
  },
  server: {
    host: "0.0.0.0",
    port: 19095,
    strictPort: true,
    proxy: {
      "/localcompose.v1.DaemonService": {
        target: "http://127.0.0.1:9090",
      },
      "/ws": {
        target: "http://127.0.0.1:9090",
        ws: true,
      },
    },
  },
});
