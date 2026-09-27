import path from "path"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vitest/config"
import { viteSingleFile } from "vite-plugin-singlefile"

export default defineConfig({
  plugins: [react(), tailwindcss(), viteSingleFile()],
  resolve: { alias: { "@": path.resolve(import.meta.dirname, "./src") } },
  test: {
    coverage: {
      provider: "v8",
      reporter: ["text", "json-summary"],
      thresholds: { statements: 60, branches: 50, functions: 55, lines: 68 },
    },
  },
  build: {
    emptyOutDir: true,
    rollupOptions: { input: path.resolve(import.meta.dirname, "index.html") },
    outDir: path.resolve(import.meta.dirname, "../internal/interface/web/dist"),
  },
})
