import { defineConfig } from "vite";
import solid from "vite-plugin-solid";

export default defineConfig({
  plugins: [solid()],
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: true,
  },
  server: {
    proxy: {
      "/ws": "http://127.0.0.1:9090",
    },
  },
});
