#!/usr/bin/env node
import { access } from "node:fs/promises"
import { delimiter, dirname, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import process from "node:process"
import { spawnSync } from "node:child_process"

process.noDeprecation = true

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..")
const args = process.argv.slice(2)
const options = { installDeps: true, skipFrontend: false }

for (const arg of args) {
  if (arg === "--no-deps") options.installDeps = false
  else if (arg === "--skip-frontend") options.skipFrontend = true
  else if (arg === "--help" || arg === "-h") {
    console.log(`Usage: make install-local ARGS="[--no-deps] [--skip-frontend]"

Cross-platform local build/install for Linux, Windows, and macOS.

Default flow:
  1. pnpm --dir frontend install --frozen-lockfile
  2. pnpm --dir frontend build
  3. go build -o <GOBIN>/cm .

Options:
  --no-deps       Skip pnpm install.
  --skip-frontend Use existing internal/interface/web/dist assets and skip pnpm install/build.
  -h, --help      Show this help.`)
    process.exit(0)
  } else fail(`unknown argument: ${arg}`)
}

const go = process.platform === "win32" ? "go.exe" : "go"

await requireFile("go.mod")

console.log(`[INFO] repository: ${root}`)
console.log(`[INFO] platform: ${process.platform}/${process.arch}`)

if (options.skipFrontend) {
  await requireFile("internal/interface/web/dist/index.html")
} else {
  await requireFile("frontend/package.json")
  await requireFile("frontend/pnpm-lock.yaml")
  if (options.installDeps) run("pnpm", ["--dir", "frontend", "install", "--frozen-lockfile"])
  run("pnpm", ["--dir", "frontend", "build"])
  await requireFile("internal/interface/web/dist/index.html")
}

const binaryPath = installedBinaryPath()
run(go, ["build", "-o", binaryPath, "."])
console.log(`[OK] installed: ${binaryPath}`)

async function requireFile(relative) {
  try {
    await access(resolve(root, relative))
  } catch {
    fail(`required file not found: ${relative}`)
  }
}

function run(command, commandArgs) {
  console.log(`[RUN] ${command} ${commandArgs.join(" ")}`)
  const result = spawnSync(command, commandArgs, { cwd: root, stdio: "inherit", windowsHide: true })
  if (result.error) fail(`${command}: ${result.error.message}`)
  if (result.status !== 0) fail(`${command} exited with code ${result.status}`)
}

function capture(command, commandArgs) {
  const result = spawnSync(command, commandArgs, { cwd: root, encoding: "utf8", windowsHide: true })
  if (result.error || result.status !== 0) return ""
  return result.stdout.trim()
}

function installedBinaryPath() {
  const name = process.platform === "win32" ? "cm.exe" : "cm"
  const gobin = capture(go, ["env", "GOBIN"])
  if (gobin) return resolve(gobin, name)
  const gopath = capture(go, ["env", "GOPATH"]).split(delimiter).filter(Boolean)[0]
  return gopath ? resolve(gopath, "bin", name) : name
}

function fail(message) {
  console.error(`[FAIL] ${message}`)
  process.exit(1)
}
