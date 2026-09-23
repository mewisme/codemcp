import { access, cp, mkdir, rm } from "node:fs/promises"
import { dirname, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import process from "node:process"
import { spawnSync } from "node:child_process"

process.noDeprecation = true

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const source = resolve(root, "web/dist")
const target = resolve(root, "internal/interface/web/dist")
const args = process.argv.slice(2)
const options = { installDeps: true, fromDist: false }

for (const arg of args) {
  if (arg === "--no-deps") options.installDeps = false
  else if (arg === "--from-dist") options.fromDist = true
  else if (arg === "--help" || arg === "-h") {
    console.log(`Usage: node scripts/prepare-web-embed.mjs [--no-deps] [--from-dist]\n\nBuild and prepare the embedded Admin UI.\n\nDefault flow:\n  1. pnpm --dir web install --frozen-lockfile\n  2. pnpm --dir web build\n  3. copy web/dist -> internal/interface/web/dist\n\nOptions:\n  --no-deps    Skip pnpm install but still build the web app.\n  --from-dist  Use the existing web/dist and skip install/build.\n  -h, --help   Show this help.`)
    process.exit(0)
  } else fail(`unknown argument: ${arg}`)
}

await requireFile("web/package.json")
await requireFile("web/pnpm-lock.yaml")
if (options.fromDist) await requireFile("web/dist/index.html")

if (!options.fromDist) {
  if (options.installDeps) run("pnpm", ["--dir", "web", "install", "--frozen-lockfile"])
  run("pnpm", ["--dir", "web", "build"])
}

await access(resolve(source, "index.html"))
await rm(target, { recursive: true, force: true })
await mkdir(target, { recursive: true })
await cp(source, target, { recursive: true })
await access(resolve(target, "index.html"))

console.log("[OK] web embed prepared")

async function requireFile(relative) {
  try {
    await access(resolve(root, relative))
  } catch {
    fail(`required file not found: ${relative}`)
  }
}

function run(command, commandArgs) {
  console.log(`[RUN] ${command} ${commandArgs.join(" ")}`)
  const result = spawnSync(command, commandArgs, { cwd: root, stdio: "inherit", windowsHide: true, shell: true })
  if (result.error) fail(`${command}: ${result.error.message}`)
  if (result.status !== 0) fail(`${command} exited with code ${result.status}`)
}

function fail(message) {
  console.error(`[FAIL] ${message}`)
  process.exit(1)
}
