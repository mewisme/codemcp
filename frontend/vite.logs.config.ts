import path from "path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

export default defineConfig({
  base: "/logs/",
  plugins: [react(), tailwindcss()],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "./src") } },
  build: {
    emptyOutDir: true,
    rollupOptions: { input: path.resolve(import.meta.dirname, "logs.html") },
    outDir: path.resolve(import.meta.dirname, "../internal/telegram/logs-dist"),
  },
})
