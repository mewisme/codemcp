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
const env = { ...process.env, HOME: home, USERPROFILE: home }
delete env.CM_TOOL_CONTEXT
const installRoot = path.join(home, "managed-install")
const installBin = process.platform === "win32" ? path.join(installRoot, "current") : path.join(home, "bin")
env.CM_INSTALL_DIR = installRoot
env.CM_BIN_DIR = installBin
const configDir = path.join(home, "config")
const defaultConfigDir = path.join(home, ".cm")
const defaultSentinel = path.join(defaultConfigDir, "release-smoke-sentinel")
const allowedDir = path.join(home, "allowed")
const globalArgs = ["--config-dir", configDir]
const serverPort = await freePort()
const adminPort = await freePort()
let child = null
let follower = null
let occupied = null

try {
  await mkdir(defaultConfigDir, { recursive: true })
  await mkdir(allowedDir, { recursive: true })
  await writeFile(defaultSentinel, "keep\n")
  run(["install", "--force"], { quiet: true })
  run(["install", "--force"], { quiet: true })
  await verifySelfInstall()
  const rootHelp = run([], { quiet: true })
  if (!rootHelp.includes("Usage:\n  cm [command]") || !/\bserve\s+Start the MCP server/.test(rootHelp)) {
    fail(`bare cm did not render root help:\n${rootHelp}`)
  }
  run(["--help"])
  const tuiHelp = run(["tui", "--help"], { quiet: true })
  if (!tuiHelp.includes("Open the full-screen CodeMCP command center")) fail(`tui help is missing command-center guidance:\n${tuiHelp}`)
  const tuiNonTTY = runExpectFailure(["tui"])
  if (!tuiNonTTY.includes("requires terminal stdin and stdout")) fail(`tui non-TTY refusal is unclear:\n${tuiNonTTY}`)
  run(["serve", "--help"])
  run(["auth", "mcp", "--help"])
  run(["workspace", "access", "--help"])
  run(["version"])
  run(["init"], { quiet: true })
  run(["config", "set", "permissions.allow_dirs", allowedDir])
  run(["config", "get", "permissions.allow_dirs"])
  run(["config", "verify"])
  run(["config", "set", "server.allow_insecure_http", "true"])
  run(["config", "set", "server.expose", "0.0.0.0"])
  run(["config", "verify"])
  run(["config", "set", "server.expose", "false"])
  run(["config", "set", "server.allow_insecure_http", "false"])
  run(["config", "set", "server.port", String(serverPort)])
  run(["config", "set", "admin.port", String(adminPort)])
  run(["config", "set", "server.allow_unauthenticated_loopback", "true"])
  run(["config", "set", "auth.mcp_enabled", "false"])
  run(["config", "set", "auth.admin_enabled", "false"])
  run(["config", "set", "integrations.ponytail.active", "false"])
  run(["config", "set", "integrations.ponytail.mode", "ultra"])
  run(["config", "set", "integrations.caveman.active", "false"])
  run(["config", "set", "integrations.caveman.mode", "wenyan-ultra"])
  run(["config", "set", "integrations.caveman.active", "true"])
  run(["config", "verify"])
  run(["status"])

  child = spawn(binary, [...globalArgs, "serve"], { env, stdio: ["ignore", "pipe", "pipe"], windowsHide: true })
  let stdout = ""
  let stderr = ""
  child.stdout.on("data", (chunk) => { stdout += chunk.toString() })
  child.stderr.on("data", (chunk) => { stderr += chunk.toString() })

  await waitForHealth(`http://127.0.0.1:${serverPort}/health`, child, () => `${stdout}\n${stderr}`)
  await waitForHealth(`http://127.0.0.1:${adminPort}/api/health`, child, () => `${stdout}\n${stderr}`)
  const workspaceID = await registerWorkspace(adminPort, allowedDir)
  await verifyActivitySSE(adminPort)
  await verifyMCP(serverPort, workspaceID, false, "off", true, "wenyan-ultra")
  await verifyWorkspaceContainerMCP(serverPort, workspaceID)
  const foregroundStatus = run(["status"], { quiet: true })
  for (const expected of ["✓ CodeMCP is running", "session     run_", "mode        foreground", "OpenAI Secure MCP Tunnel is disabled"]) {
    if (!foregroundStatus.includes(expected)) fail(`foreground status missing ${JSON.stringify(expected)}:\n${foregroundStatus}`)
  }

  const servePID = child.pid
  const reloadedServerPort = await freePort()
  const reloadedAdminPort = await freePort()
  run(["config", "set", "server.port", String(reloadedServerPort)])
  run(["config", "set", "admin.port", String(reloadedAdminPort)])
  run(["config", "set", "integrations.ponytail.active", "true"])
  run(["config", "set", "integrations.ponytail.mode", "lite"])
  run(["config", "set", "integrations.caveman.mode", "full"])
  if (child.pid !== servePID || child.exitCode !== null) fail("automatic config reload restarted or stopped the serve process")
  await waitForHealth(`http://127.0.0.1:${reloadedServerPort}/health`, child, () => `${stdout}\n${stderr}`)
  await waitForHealth(`http://127.0.0.1:${reloadedAdminPort}/api/health`, child, () => `${stdout}\n${stderr}`)
  await verifyMCP(reloadedServerPort, workspaceID, true, "lite", true, "full")

  occupied = await occupyPort()
  runExpectFailure(["config", "set", "server.port", String(occupied.port)])
  if (child.pid !== servePID || child.exitCode !== null) fail("failed automatic config reload stopped the serve process")
  await waitForHealth(`http://127.0.0.1:${reloadedServerPort}/health`, child, () => `${stdout}\n${stderr}`)
  await closeServer(occupied.server)
  occupied = null

  await stopRuntimeChild(child)
  child = null
  runExpectFailure(["config", "reload"])

  const history = run(["logs", "--debug", "--event", "server.*", "--tail", "200"], { quiet: true })
  if (!history.includes("server.ready") && !history.includes("Server ready")) fail(`runtime history missing server readiness event:\n${history}`)
  if (!history.includes("── session run_")) fail(`runtime history missing session boundary:\n${history}`)
  if (!/^\d{2}:\d{2}:\d{2} /m.test(history)) fail(`runtime history missing replay timestamp:\n${history}`)
  const noTimeHistory = run(["logs", "--event", "server.*", "--tail", "10", "--no-time"], { quiet: true })
  if (/^\d{2}:\d{2}:\d{2} /m.test(noTimeHistory)) fail(`--no-time still rendered replay timestamp:\n${noTimeHistory}`)
  const sessionLifecycle = run(["logs", "--event", "runtime.session.*", "--tail", "10"], { quiet: true })
  if (!sessionLifecycle.includes("Runtime session ended")) fail(`runtime history missing session end marker:\n${sessionLifecycle}`)
  const logPath = run(["logs", "path"], { quiet: true })
  if (!logPath.includes(path.join(configDir, "logs", "runtime.jsonl"))) fail(`logs path does not use isolated config root:\n${logPath}`)

  const managedServiceID = "release-smoke-managed"
  child = spawn(binary, [...globalArgs, "_service", "run", "--service-id", managedServiceID, "--service-scope", "user"], { env, stdio: ["ignore", "pipe", "pipe"], windowsHide: true })
  stdout = ""
  stderr = ""
  child.stdout.on("data", (chunk) => { stdout += chunk.toString() })
  child.stderr.on("data", (chunk) => { stderr += chunk.toString() })
  await waitForHealth(`http://127.0.0.1:${reloadedServerPort}/health`, child, () => `${stdout}\n${stderr}`)
  await waitForHealth(`http://127.0.0.1:${reloadedAdminPort}/api/health`, child, () => `${stdout}\n${stderr}`)

  const managedStatus = await waitForStatus(child, () => `${stdout}\n${stderr}`)
  for (const expected of ["✓ CodeMCP is running", "managed     user ·", `service     ${managedServiceID}`, "session     run_", "OpenAI Secure MCP Tunnel is disabled"]) {
    if (!managedStatus.includes(expected)) fail(`managed status missing ${JSON.stringify(expected)}:\n${managedStatus}`)
  }
  const managedLogs = run(["logs", "--debug", "--event", "server.*", "--grep", "Server", "--tail", "50"], { quiet: true })
  if (!managedLogs.includes("server.ready") && !managedLogs.includes("Server ready")) fail(`managed runtime logs filter returned no server event:\n${managedLogs}`)
  const managedJSON = run(["--log-format=json", "logs", "--event", "server.ready", "--tail", "1"], { quiet: true })
  const managedJSONEvent = JSON.parse(managedJSON.split(/\r?\n/).filter(Boolean).at(-1))
  if (typeof managedJSONEvent.run_id !== "string" || !managedJSONEvent.run_id.startsWith("run_") || managedJSONEvent.service_scope !== "user") {
    fail(`managed JSON logs missing session/service metadata:\n${managedJSON}`)
  }

  run(["logs", "clear", "--force"], { quiet: true })
  let followerStdout = ""
  let followerStderr = ""
  follower = spawn(binary, [...globalArgs, "logs", "--follow", "--event", "config.reloaded", "--tail", "0"], { env, stdio: ["ignore", "pipe", "pipe"], windowsHide: true })
  follower.stdout.on("data", (chunk) => { followerStdout += chunk.toString() })
  follower.stderr.on("data", (chunk) => { followerStderr += chunk.toString() })
  await sleep(250)
  run(["config", "set", "integrations.caveman.mode", "ultra"], { quiet: true })
  await waitForText("runtime log follow", follower, () => `${followerStdout}\n${followerStderr}`, "Configuration reloaded")
  await stopChild(follower)
  follower = null

  await stopRuntimeChild(child)
  child = null
  const stoppedLogs = run(["logs", "--event", "config.reloaded", "--tail", "10"], { quiet: true })
  if (!stoppedLogs.includes("Configuration reloaded")) fail(`persisted logs unavailable after managed runtime stopped:\n${stoppedLogs}`)

  run(["uninit"], { quiet: true })
  if (await readFile(defaultSentinel, "utf8") !== "keep\n") fail("isolated commands modified the default config root")
  console.log("[OK] release smoke: self-install -> init -> verify -> config -> serve/reload/rollback -> MCP -> logs -> managed runtime -> follow/clear -> stop -> uninit")
} finally {
  if (occupied) await closeServer(occupied.server).catch(() => undefined)
  if (follower) await stopChild(follower).catch(() => undefined)
  if (child) await stopChild(child).catch(() => undefined)
  await rm(home, { recursive: true, force: true })
}

function run(args, { quiet = false } = {}) {
  if (globalArgs[0] !== "--config-dir" || !globalArgs[1] || path.resolve(globalArgs[1]) === path.resolve(defaultConfigDir)) {
    fail("runtime smoke requires an explicit isolated --config-dir")
  }
  const result = spawnSync(binary, [...globalArgs, ...args], { env, encoding: "utf8", windowsHide: true })
  if (result.error) fail(`${args.join(" ")}: ${result.error.message}`)
  if (result.status !== 0) {
    const output = [result.stdout, result.stderr].filter(Boolean).join("\n").trim()
    fail(`${args.join(" ")} failed with exit code ${result.status}${output ? `\n${output}` : ""}`)
  }
  if (!quiet) {
    const output = [result.stdout, result.stderr].filter(Boolean).join("").trim()
    if (output) console.log(output)
  }
  return [result.stdout, result.stderr].filter(Boolean).join("").trim()
}

async function verifySelfInstall() {
  const metadata = JSON.parse(await readFile(path.join(installRoot, "install.json"), "utf8"))
  if (metadata.method !== "direct" || typeof metadata.version !== "string" || !metadata.version || path.resolve(metadata.install_dir) !== path.resolve(installRoot)) {
    fail(`self-install metadata mismatch: ${JSON.stringify(metadata)}`)
  }
  const executable = path.join(installRoot, "current", process.platform === "win32" ? "cm.exe" : "cm")
  const versionResult = spawnSync(executable, ["version"], { env, encoding: "utf8", windowsHide: true })
  if (versionResult.error || versionResult.status !== 0) fail(`installed binary is not executable: ${versionResult.error?.message || versionResult.stderr}`)
  const versionOutput = [versionResult.stdout, versionResult.stderr].filter(Boolean).join("\n")
  if (!versionOutput.includes(metadata.version)) fail(`self-install metadata version does not match installed binary: ${JSON.stringify(metadata)}`)
  const canonical = process.platform === "win32" ? executable : path.join(installBin, "cm")
  if (await realpath(canonical) !== await realpath(executable)) fail(`canonical command does not resolve to current binary: ${canonical}`)
  const legacyNames = process.platform === "win32"
    ? ["chatgpt-mcp.exe", "cgm.cmd", "cmcp.cmd"]
    : ["chatgpt-mcp", "cgm", "cmcp"]
  for (const legacy of legacyNames) {
    const legacyPath = path.join(installBin, legacy)
    try {
      await realpath(legacyPath)
      fail(`install unexpectedly created legacy executable or alias: ${legacyPath}`)
    } catch (error) {
      if (error?.code !== "ENOENT") throw error
    }
  }
}

function runExpectFailure(args) {
  if (globalArgs[0] !== "--config-dir" || !globalArgs[1] || path.resolve(globalArgs[1]) === path.resolve(defaultConfigDir)) {
    fail("runtime smoke requires an explicit isolated --config-dir")
  }
  const result = spawnSync(binary, [...globalArgs, ...args], { env, encoding: "utf8", windowsHide: true })
  if (result.error) fail(`${args.join(" ")}: ${result.error.message}`)
  if (result.status === 0) fail(`${args.join(" ")} unexpectedly succeeded`)
  return [result.stdout, result.stderr].filter(Boolean).join("").trim()
}

async function verifyMCP(port, workspaceID, ponytailActive, ponytailMode, cavemanActive, cavemanMode) {
  const discover = await mcpRequest(port, "server/discover", {}, 1)
  assertStatus(discover.response, 200, "server/discover")
  if (discover.response.headers.get("mcp-session-id")) fail("modern MCP response unexpectedly returned Mcp-Session-Id")
  if (!Array.isArray(discover.body?.result?.supportedVersions) || !discover.body.result.supportedVersions.includes(protocolVersion)) {
    fail(`server/discover did not advertise ${protocolVersion}: ${JSON.stringify(discover.body)}`)
  }

  const tools = await mcpRequest(port, "tools/list", {}, 2)
  assertStatus(tools.response, 200, "tools/list")
  if (!Array.isArray(tools.body?.result?.tools) || tools.body.result.tools.length === 0) {
    fail(`tools/list returned no tools: ${JSON.stringify(tools.body)}`)
  }
  const toolNames = new Set(tools.body.result.tools.map((tool) => tool?.name))
  if (!toolNames.has("get_version")) fail(`get_version missing from tools/list: ${JSON.stringify(tools.body)}`)
  if (!toolNames.has("request_control_approval")) fail(`request_control_approval missing from tools/list: ${JSON.stringify(tools.body)}`)
  for (const name of ["workspace_container_list", "workspace_container_status", "workspace_container_context"]) {
    if (!toolNames.has(name)) fail(`${name} missing from tools/list: ${JSON.stringify(tools.body)}`)
  }
  if (!toolNames.has("ponytail_turn")) fail(`ponytail_turn missing from tools/list: ${JSON.stringify(tools.body)}`)
  if (!toolNames.has("caveman_turn")) fail(`caveman_turn missing from tools/list: ${JSON.stringify(tools.body)}`)
  if (!Number.isFinite(tools.body.result.ttlMs) || typeof tools.body.result.cacheScope !== "string") {
    fail(`tools/list cache hints are missing: ${JSON.stringify(tools.body)}`)
  }

  const version = await mcpRequest(port, "tools/call", { name: "get_version", arguments: {} }, 3)
  assertStatus(version.response, 200, "get_version")
  const versionInfo = version.body?.result?.structuredContent
  if (!versionInfo || typeof versionInfo.version !== "string" || typeof versionInfo.commit !== "string" || typeof versionInfo.build_time !== "string") {
    fail(`get_version returned invalid structured content: ${JSON.stringify(version.body)}`)
  }

  const ponytail = await mcpRequest(port, "tools/call", { name: "ponytail_turn", arguments: { workspace_id: workspaceID, prompt: "continue", action: "refresh" } }, 4)
  assertStatus(ponytail.response, 200, "ponytail_turn")
  const ponytailState = ponytail.body?.result?.structuredContent
  if (!ponytailState || ponytailState.available !== true || ponytailState.active !== ponytailActive || ponytailState.mode !== ponytailMode) {
    fail(`ponytail_turn state mismatch: expected active=${ponytailActive} mode=${ponytailMode}, got ${JSON.stringify(ponytail.body)}`)
  }
  if (ponytailActive && (typeof ponytailState.active_instructions !== "string" || !ponytailState.active_instructions.includes("PONYTAIL MODE ACTIVE") || !ponytailState.active_instructions.includes("## The ladder"))) {
    fail(`ponytail_turn did not return built-in instructions: ${JSON.stringify(ponytail.body)}`)
  }
  if (!ponytailActive && ponytailState.active_instructions) fail(`inactive ponytail_turn returned instructions: ${JSON.stringify(ponytail.body)}`)

  const caveman = await mcpRequest(port, "tools/call", { name: "caveman_turn", arguments: { workspace_id: workspaceID, prompt: "continue", action: "refresh" } }, 5)
  assertStatus(caveman.response, 200, "caveman_turn")
  const cavemanState = caveman.body?.result?.structuredContent
  if (!cavemanState || cavemanState.available !== true || cavemanState.active !== cavemanActive || cavemanState.mode !== cavemanMode) {
    fail(`caveman_turn state mismatch: expected active=${cavemanActive} mode=${cavemanMode}, got ${JSON.stringify(caveman.body)}`)
  }
  if (cavemanActive && (typeof cavemanState.active_instructions !== "string" || !cavemanState.active_instructions.includes("CAVEMAN MODE ACTIVE") || !cavemanState.active_instructions.includes("## Rules"))) {
    fail(`caveman_turn did not return built-in instructions: ${JSON.stringify(caveman.body)}`)
  }
  if (!cavemanActive && cavemanState.active_instructions) fail(`inactive caveman_turn returned instructions: ${JSON.stringify(caveman.body)}`)

  const legacy = await mcpRequest(port, "initialize", {}, 6)
  assertStatus(legacy.response, 404, "initialize")
  if (legacy.body?.error?.code !== -32601) {
    fail(`initialize error code = ${legacy.body?.error?.code}, want -32601`)
  }
}

async function verifyWorkspaceContainerMCP(port, workspaceID) {
  const name = "Release smoke container"
  run(["workspace", "container", "create", name], { quiet: true })
  const persisted = JSON.parse(run(["workspace", "container", "list", "--json"], { quiet: true }))
  const created = persisted.find((container) => container?.name === name)
  if (typeof created?.id !== "string" || !created.id.startsWith("wsc_")) fail(`container create returned no persisted wsc id: ${JSON.stringify(persisted)}`)
  const containerID = created.id

  let status = await mcpRequest(port, "tools/call", { name: "workspace_container_status", arguments: { container_id: containerID } }, 40)
  assertStatus(status.response, 200, "workspace_container_status after create")
  let value = status.body?.result?.structuredContent
  if (!value || value.container_id !== containerID || value.name !== name || value.workspace_count !== 0) {
    fail(`container MCP status is stale after create: ${JSON.stringify(status.body)}`)
  }

  const listed = await mcpRequest(port, "tools/call", { name: "workspace_container_list", arguments: {} }, 41)
  assertStatus(listed.response, 200, "workspace_container_list after create")
  const listValue = listed.body?.result?.structuredContent
  if (!Array.isArray(listValue?.containers) || !listValue.containers.some((container) => container?.container_id === containerID)) {
    fail(`container MCP list is stale after create: ${JSON.stringify(listed.body)}`)
  }

  run(["workspace", "container", "rename", containerID, "Release smoke renamed"], { quiet: true })
  status = await mcpRequest(port, "tools/call", { name: "workspace_container_status", arguments: { container_id: containerID } }, 42)
  assertStatus(status.response, 200, "workspace_container_status after rename")
  value = status.body?.result?.structuredContent
  if (value?.name !== "Release smoke renamed") fail(`container MCP status is stale after rename: ${JSON.stringify(status.body)}`)

  run(["workspace", "container", "add", containerID, workspaceID], { quiet: true })
  let context = await mcpRequest(port, "tools/call", { name: "workspace_container_context", arguments: { container_id: containerID } }, 43)
  assertStatus(context.response, 200, "workspace_container_context after add")
  let contextValue = context.body?.result?.structuredContent
  if (contextValue?.workspace_count !== 1 || contextValue?.workspaces?.[0]?.workspace_id !== workspaceID) {
    fail(`container MCP context is stale after membership add: ${JSON.stringify(context.body)}`)
  }

  run(["workspace", "container", "remove", containerID, workspaceID], { quiet: true })
  context = await mcpRequest(port, "tools/call", { name: "workspace_container_context", arguments: { container_id: containerID } }, 44)
  assertStatus(context.response, 200, "workspace_container_context after remove")
  contextValue = context.body?.result?.structuredContent
  if (contextValue?.workspace_count !== 0 || !Array.isArray(contextValue?.workspaces) || contextValue.workspaces.length !== 0) {
    fail(`container MCP context is stale after membership remove: ${JSON.stringify(context.body)}`)
  }

  run(["workspace", "container", "delete", containerID], { quiet: true })
  const afterDelete = await mcpRequest(port, "tools/call", { name: "workspace_container_list", arguments: {} }, 45)
  assertStatus(afterDelete.response, 200, "workspace_container_list after delete")
  const afterDeleteValue = afterDelete.body?.result?.structuredContent
  if (!Array.isArray(afterDeleteValue?.containers) || afterDeleteValue.containers.some((container) => container?.container_id === containerID)) {
    fail(`container MCP list is stale after delete: ${JSON.stringify(afterDelete.body)}`)
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

async function verifyActivitySSE(port) {
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), 5000)
  try {
    const response = await fetch(`http://127.0.0.1:${port}/api/activity/stream?history=0`, { signal: controller.signal })
    assertStatus(response, 200, "activity SSE")
    if (!response.body || !response.headers.get("content-type")?.includes("text/event-stream")) fail("activity SSE response is not an event stream")
    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ""
    while (!buffer.includes("\n\n")) {
      const { value, done } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
    }
    await reader.cancel()
    if (!buffer.includes("event: ready\n") || !buffer.includes('"latest_sequence":')) fail(`activity SSE did not emit ready control event: ${JSON.stringify(buffer)}`)
  } finally {
    clearTimeout(timeout)
    controller.abort()
  }
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

async function occupyPort() {
  return await new Promise((resolve, reject) => {
    const server = createServer()
    server.once("error", reject)
    server.listen(0, "127.0.0.1", () => {
      const address = server.address()
      const port = typeof address === "object" && address ? address.port : 0
      if (!port) {
        server.close()
        reject(new Error("failed to reserve occupied port"))
        return
      }
      resolve({ server, port })
    })
  })
}

async function closeServer(server) {
  await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()))
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

async function waitForStatus(server, output) {
  const deadline = Date.now() + 10000
  let last = ""
  while (Date.now() < deadline) {
    if (server.exitCode !== null) fail(`serve exited before runtime status became ready with code ${server.exitCode}\n${output().trim()}`)
    const result = spawnSync(binary, [...globalArgs, "status"], { env, encoding: "utf8", windowsHide: true })
    last = [result.stdout, result.stderr].filter(Boolean).join("").trim()
    if (!result.error && result.status === 0 && last.includes("✓ CodeMCP is running")) return last
    await sleep(50)
  }
  fail(`runtime status did not become ready:\n${last}\n${output().trim()}`)
}

async function waitForText(label, processHandle, output, expected) {
  const deadline = Date.now() + 10000
  while (Date.now() < deadline) {
    const text = output()
    if (text.includes(expected)) return
    if (processHandle.exitCode !== null) fail(`${label} exited with code ${processHandle.exitCode} before ${JSON.stringify(expected)} appeared\n${text.trim()}`)
    await sleep(50)
  }
  fail(`${label} timed out waiting for ${JSON.stringify(expected)}\n${output().trim()}`)
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
  await Promise.race([
    new Promise((resolve) => server.once("exit", resolve)),
    sleep(2000),
  ])
}

async function stopRuntimeChild(server) {
  if (server.exitCode !== null) return
  const controlPath = path.join(configDir, ".runtime-control.json")
  let control
  try {
    control = JSON.parse(await readFile(controlPath, "utf8"))
  } catch (error) {
    fail(`runtime control state unavailable before graceful shutdown: ${error instanceof Error ? error.message : String(error)}`)
  }
  if (!control?.address || !control?.token || control?.pid !== server.pid) {
    fail(`runtime control state does not match child pid ${server.pid}: ${JSON.stringify({ pid: control?.pid, address: control?.address })}`)
  }
  let response
  try {
    response = await fetch(`http://${control.address}/shutdown`, {
      method: "POST",
      headers: { Authorization: `Bearer ${control.token}` },
      signal: AbortSignal.timeout(5000),
    })
  } catch (error) {
    fail(`runtime graceful shutdown request failed: ${error instanceof Error ? error.message : String(error)}`)
  }
  if (!response.ok) fail(`runtime graceful shutdown returned HTTP ${response.status}`)
  const exited = await Promise.race([
    new Promise((resolve) => server.once("exit", () => resolve(true))),
    sleep(5000).then(() => false),
  ])
  if (!exited) fail(`runtime did not exit after graceful shutdown: pid ${server.pid}`)
}

function sleep(ms) { return new Promise((resolve) => setTimeout(resolve, ms)) }
function fail(message) { console.error(`[FAIL] ${message}`); process.exit(1) }
