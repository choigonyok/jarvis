import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5180,
    proxy: { "/api": { target: "http://127.0.0.1:8788", ws: true } },
  },
  build: { outDir: "dist", emptyOutDir: true },
});
