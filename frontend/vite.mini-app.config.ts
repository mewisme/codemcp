import path from "path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

export default defineConfig({
  cacheDir: "node_modules/.vite-mini-app",
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "./src") } },
  build: {
    emptyOutDir: true,
    rollupOptions: {
      input: path.resolve(import.meta.dirname, "mini-app.html"),
    },
    outDir: path.resolve(
      import.meta.dirname,
      "../internal/telegram/mini-app-dist"
    ),
  },
})
