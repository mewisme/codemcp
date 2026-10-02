import { mkdir, mkdtemp, readFile, realpath, rm, writeFile } from "node:fs/promises"
import { createServer } from "node:net"
import { tmpdir } from "node:os"
import path from "node:path"
import process from "node:process"
import { spawn, spawnSync } from "node:child_process"

process.noDeprecation = true

const protocolVersion = "2026-07-28"
const input = process.argv[2]
if (!input) fail("usage: node scripts/release/smoke.mjs <binary>")

const binary = path.resolve(input)
const home = await mkdtemp(path.join(tmpdir(), "cm-release-smoke-"))
const env = { ...process.env, HOME: home, USERPROFILE: home, CM_TELEMETRY: "0" }
delete env.CM_TOOL_CONTEXT

const installRoot = path.join(home, "managed-install")
const installBin = process.platform === "win32" ? path.join(installRoot, "current") : path.join(home, "bin")
const configDir = path.join(home, "config")
const defaultConfigDir = path.join(home, ".cm")
const defaultSentinel = path.join(defaultConfigDir, "release-smoke-sentinel")
const allowedDir = path.join(home, "allowed")
const globalArgs = ["--config-dir", configDir]
const serverPort = await freePort()
let adminPort = await freePort()
while (adminPort === serverPort) adminPort = await freePort()
let child = null

env.CM_INSTALL_DIR = installRoot
env.CM_BIN_DIR = installBin

try {
  await mkdir(defaultConfigDir, { recursive: true })
  await mkdir(allowedDir, { recursive: true })
  await writeFile(defaultSentinel, "keep\n")

  const rootHelp = run([], { quiet: true })
  if (!rootHelp.includes("Usage:\n  cm [command]")) fail(`bare cm did not render root help:\n${rootHelp}`)
  run(["version"], { quiet: true })

  run(["install", "--force"], { quiet: true })
  await verifySelfInstall()

  run(["init"], { quiet: true })
  run(["config", "set", "permissions.allow_dirs", allowedDir], { quiet: true })
  run(["config", "set", "http.mcp.port", String(serverPort)], { quiet: true })
  run(["config", "set", "http.admin.port", String(adminPort)], { quiet: true })
  run(["config", "set", "http.security.allow_unauthenticated_loopback", "true"], { quiet: true })
  run(["config", "set", "http.mcp.auth.enabled", "false"], { quiet: true })
  run(["config", "set", "http.admin.auth.enabled", "false"], { quiet: true })
  run(["config", "verify"], { quiet: true })

  child = spawn(binary, [...globalArgs, "serve"], {
    env,
    stdio: ["ignore", "pipe", "pipe"],
    windowsHide: true,
  })
  let stdout = ""
  let stderr = ""
  child.stdout.on("data", (chunk) => { stdout += chunk.toString() })
  child.stderr.on("data", (chunk) => { stderr += chunk.toString() })

  await waitForHealth(`http://127.0.0.1:${serverPort}/health`, child, () => `${stdout}\n${stderr}`)
  await waitForHealth(`http://127.0.0.1:${adminPort}/api/health`, child, () => `${stdout}\n${stderr}`)

  const workspaceID = await registerWorkspace(adminPort, allowedDir)
  await verifyMCP(serverPort, workspaceID)

  await stopRuntimeChild(child)
  child = null

  run(["uninit"], { quiet: true })
  if (await readFile(defaultSentinel, "utf8") !== "keep\n") {
    fail("isolated release smoke modified the default config root")
  }

  console.log("[OK] release smoke: binary -> install -> init -> serve -> health -> MCP -> graceful shutdown -> uninit")
} finally {
  if (child) await stopChild(child).catch(() => undefined)
  await rm(home, { recursive: true, force: true })
}

function run(args, { quiet = false } = {}) {
  if (globalArgs[0] !== "--config-dir" || !globalArgs[1] || path.resolve(globalArgs[1]) === path.resolve(defaultConfigDir)) {
    fail("release smoke requires an explicit isolated --config-dir")
  }
  const result = spawnSync(binary, [...globalArgs, ...args], { env, encoding: "utf8", windowsHide: true })
  if (result.error) fail(`${args.join(" ")}: ${result.error.message}`)
  if (result.status !== 0) {
    const output = [result.stdout, result.stderr].filter(Boolean).join("\n").trim()
    fail(`${args.join(" ")} failed with exit code ${result.status}${output ? `\n${output}` : ""}`)
  }
  const output = [result.stdout, result.stderr].filter(Boolean).join("").trim()
  if (!quiet && output) console.log(output)
  return output
}

async function verifySelfInstall() {
  const metadata = JSON.parse(await readFile(path.join(installRoot, "install.json"), "utf8"))
  if (metadata.method !== "direct" || typeof metadata.version !== "string" || !metadata.version) {
    fail(`self-install metadata mismatch: ${JSON.stringify(metadata)}`)
  }

  const executable = path.join(installRoot, "current", process.platform === "win32" ? "cm.exe" : "cm")
  const versionResult = spawnSync(executable, ["version"], { env, encoding: "utf8", windowsHide: true })
  if (versionResult.error || versionResult.status !== 0) {
    fail(`installed binary is not executable: ${versionResult.error?.message || versionResult.stderr}`)
  }

  const versionOutput = [versionResult.stdout, versionResult.stderr].filter(Boolean).join("\n")
  if (!versionOutput.includes(metadata.version)) {
    fail(`self-install metadata version does not match installed binary: ${JSON.stringify(metadata)}`)
  }

  const canonical = process.platform === "win32" ? executable : path.join(installBin, "cm")
  if (await realpath(canonical) !== await realpath(executable)) {
    fail(`canonical command does not resolve to current binary: ${canonical}`)
  }
}

async function verifyMCP(port, workspaceID) {
  const discover = await mcpRequest(port, "server/discover", {}, 1)
  assertStatus(discover.response, 200, "server/discover")
  if (!Array.isArray(discover.body?.result?.supportedVersions) || !discover.body.result.supportedVersions.includes(protocolVersion)) {
    fail(`server/discover did not advertise ${protocolVersion}: ${JSON.stringify(discover.body)}`)
  }

  const tools = await mcpRequest(port, "tools/list", {}, 2)
  assertStatus(tools.response, 200, "tools/list")
  const names = new Set((tools.body?.result?.tools || []).map((tool) => tool?.name))
  if (!names.has("get_version")) fail(`get_version missing from tools/list: ${JSON.stringify(tools.body)}`)

  const version = await mcpRequest(port, "tools/call", { name: "get_version", arguments: {} }, 3)
  assertStatus(version.response, 200, "get_version")
  const value = version.body?.result?.structuredContent
  if (!value || typeof value.version !== "string") {
    fail(`get_version returned invalid structured content: ${JSON.stringify(version.body)}`)
  }

  const workspace = await mcpRequest(port, "tools/call", {
    name: "workspace_status",
    arguments: { workspace_id: workspaceID },
  }, 4)
  assertStatus(workspace.response, 200, "workspace_status")
  if (workspace.body?.result?.isError === true) {
    fail(`workspace_status returned tool error: ${JSON.stringify(workspace.body)}`)
  }
}

async function registerWorkspace(port, workspacePath) {
  const response = await fetch(`http://127.0.0.1:${port}/api/workspaces`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ path: workspacePath }),
    signal: AbortSignal.timeout(5000),
  })
  assertStatus(response, 200, "workspace register")
  const body = await response.json()
  if (typeof body?.id !== "string" || !body.id) fail(`workspace register returned invalid body: ${JSON.stringify(body)}`)
  return body.id
}

async function mcpRequest(port, method, params, id) {
  const payload = {
    jsonrpc: "2.0",
    id,
    method,
    params: {
      ...params,
      _meta: {
        "io.modelcontextprotocol/protocolVersion": protocolVersion,
        "io.modelcontextprotocol/clientCapabilities": {},
        "io.modelcontextprotocol/clientInfo": { name: "release-smoke", version: "1.0.0" },
      },
    },
  }
  const headers = {
    "Content-Type": "application/json",
    "MCP-Protocol-Version": protocolVersion,
    "Mcp-Method": method,
  }
  if (method === "tools/call" && typeof params?.name === "string") headers["Mcp-Name"] = params.name

  const response = await fetch(`http://127.0.0.1:${port}/mcp`, {
    method: "POST",
    headers,
    body: JSON.stringify(payload),
    signal: AbortSignal.timeout(5000),
  })
  let body
  try {
    body = await response.json()
  } catch (error) {
    fail(`${method} returned invalid JSON: ${error instanceof Error ? error.message : String(error)}`)
  }
  return { response, body }
}

function assertStatus(response, expected, label) {
  if (response.status !== expected) fail(`${label} status = ${response.status}, want ${expected}`)
}

async function freePort() {
  return await new Promise((resolve, reject) => {
    const server = createServer()
    server.unref()
    server.once("error", reject)
    server.listen(0, "127.0.0.1", () => {
      const address = server.address()
      const port = typeof address === "object" && address ? address.port : 0
      server.close((error) => error ? reject(error) : resolve(port))
    })
  })
}

async function waitForHealth(url, server, output) {
  const deadline = Date.now() + 15000
  while (Date.now() < deadline) {
    if (server.exitCode !== null) fail(`serve exited before health check with code ${server.exitCode}\n${output().trim()}`)
    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(1000) })
      if (response.ok) {
        const body = await response.json()
        if (body?.ok === true) return
      }
    } catch {}
    await sleep(100)
  }
  fail(`health check timed out: ${url}\n${output().trim()}`)
}

async function stopRuntimeChild(server) {
  if (server.exitCode !== null) return
  const controlPath = path.join(configDir, ".runtime-control.json")
  const control = JSON.parse(await readFile(controlPath, "utf8"))
  if (!control?.address || !control?.token || control?.pid !== server.pid) {
    fail(`runtime control state does not match child pid ${server.pid}`)
  }

  const response = await fetch(`http://${control.address}/shutdown`, {
    method: "POST",
    headers: { Authorization: `Bearer ${control.token}` },
    signal: AbortSignal.timeout(5000),
  })
  if (!response.ok) fail(`runtime graceful shutdown returned HTTP ${response.status}`)

  const exited = await Promise.race([
    new Promise((resolve) => server.once("exit", () => resolve(true))),
    sleep(5000).then(() => false),
  ])
  if (!exited) fail(`runtime did not exit after graceful shutdown: pid ${server.pid}`)
}

async function stopChild(server) {
  if (server.exitCode !== null) return
  server.kill("SIGTERM")
  const exited = await Promise.race([
    new Promise((resolve) => server.once("exit", () => resolve(true))),
    sleep(5000).then(() => false),
  ])
  if (exited) return
  server.kill("SIGKILL")
}

function sleep(ms) { return new Promise((resolve) => setTimeout(resolve, ms)) }
function fail(message) {
  console.error(`[FAIL] ${message}`)
  process.exit(1)
}
