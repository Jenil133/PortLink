import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      "/ws": { target: "ws://localhost:8080", ws: true },
      "/api": { target: "http://localhost:8080" },
    },
  },
  build: {
    target: "es2022",
    // deck.gl and MapLibre are large on their own; the warning would fire on every build.
    chunkSizeWarningLimit: 1500,
  },
});
