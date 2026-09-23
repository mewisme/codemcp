import { access, cp, mkdir, readFile, readdir, rm, stat } from "node:fs/promises"
import { dirname, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import process from "node:process"
import { spawnSync } from "node:child_process"

process.noDeprecation = true

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const source = resolve(root, "frontend/dist")
const target = resolve(root, "internal/interface/web/dist")
const args = process.argv.slice(2)
const options = { installDeps: true, fromDist: false, check: false }

for (const arg of args) {
  if (arg === "--no-deps") options.installDeps = false
  else if (arg === "--from-dist") options.fromDist = true
  else if (arg === "--check") options.check = true
  else if (arg === "--help" || arg === "-h") {
    console.log(`Usage: node scripts/prepare-frontend-embed.mjs [--no-deps] [--from-dist] [--check]\n\nBuild and prepare the embedded Admin UI.\n\nDefault flow:\n  1. pnpm --dir frontend install --frozen-lockfile\n  2. pnpm --dir frontend build\n  3. copy frontend/dist -> internal/interface/web/dist\n\nOptions:\n  --no-deps    Skip pnpm install but still build the frontend.\n  --from-dist  Use the existing frontend/dist and skip install/build.\n  --check      Verify frontend/dist matches the embedded dist without writing files.\n  -h, --help   Show this help.`)
    process.exit(0)
  } else fail(`unknown argument: ${arg}`)
}

await requireFile("frontend/package.json")
await requireFile("frontend/pnpm-lock.yaml")
if (options.fromDist || options.check) await requireFile("frontend/dist/index.html")

if (options.check) {
  await requireFile("internal/interface/web/dist/index.html")
  await verifyTreesMatch(source, target)
  console.log("[OK] frontend embed is in sync")
  process.exit(0)
}

if (!options.fromDist) {
  if (options.installDeps) run("pnpm", ["--dir", "frontend", "install", "--frozen-lockfile"])
  run("pnpm", ["--dir", "frontend", "build"])
}

await access(resolve(source, "index.html"))
await rm(target, { recursive: true, force: true })
await mkdir(target, { recursive: true })
await cp(source, target, { recursive: true })
await access(resolve(target, "index.html"))

console.log("[OK] frontend embed prepared")

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

async function verifyTreesMatch(leftRoot, rightRoot) {
  const [leftFiles, rightFiles] = await Promise.all([listFiles(leftRoot), listFiles(rightRoot)])
  if (leftFiles.join("\n") !== rightFiles.join("\n")) {
    fail("frontend embed file list is out of sync")
  }
  for (const relative of leftFiles) {
    const [leftInfo, rightInfo] = await Promise.all([
      stat(resolve(leftRoot, relative)),
      stat(resolve(rightRoot, relative)),
    ])
    if (leftInfo.size !== rightInfo.size) fail(`frontend embed differs: ${relative}`)
    const [left, right] = await Promise.all([
      readFile(resolve(leftRoot, relative)),
      readFile(resolve(rightRoot, relative)),
    ])
    if (!left.equals(right)) fail(`frontend embed differs: ${relative}`)
  }
}

async function listFiles(rootDir, relative = "") {
  const entries = await readdir(resolve(rootDir, relative), { withFileTypes: true })
  const files = []
  for (const entry of entries) {
    const child = relative ? `${relative}/${entry.name}` : entry.name
    if (entry.isDirectory()) files.push(...(await listFiles(rootDir, child)))
    else if (entry.isFile()) files.push(child)
  }
  return files.sort()
}
