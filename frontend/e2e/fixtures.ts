import { expect, type Locator, type Page } from "@playwright/test"

const longToken = "segment-".repeat(80)

export async function installAdminMocks(
  page: Page,
  options: { activityReconnect?: boolean } = {}
) {
  await page.addInitScript(
    ({ activityReconnect }) => {
      const nativeFetch = window.fetch.bind(window)
      let activityCalls = 0
      const openSSE = (packet: string) =>
        new Response(
          new ReadableStream<Uint8Array>({
            start(controller) {
              controller.enqueue(new TextEncoder().encode(packet))
            },
          }),
          { status: 200, headers: { "content-type": "text/event-stream" } }
        )
      Object.defineProperty(window, "__activityStreamCalls", {
        configurable: true,
        get: () => activityCalls,
      })
      window.fetch = async (input, init) => {
        const raw = input instanceof Request ? input.url : String(input)
        const url = new URL(raw, window.location.href)
        if (url.pathname === "/api/requests/stream") {
          return openSSE(
            'event: ready\ndata: {"requests":[],"latest_sequence":0}\n\n'
          )
        }
        if (activityReconnect && url.pathname === "/api/activity/stream") {
          activityCalls++
          if (activityCalls === 1) {
            await new Promise((resolve) => window.setTimeout(resolve, 250))
            return new Response("temporary stream failure", { status: 503 })
          }
          return openSSE('event: ready\ndata: {"latest_sequence":0}\n\n')
        }
        return nativeFetch(input, init)
      }
    },
    { activityReconnect: options.activityReconnect === true }
  )

  let activityCalls = 0
  await page.route("**/api/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const json = (value: unknown, status = 200) =>
      route.fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify(value),
      })

    if (url.pathname === "/api/health")
      return json({ ok: true, auth_enabled: false })
    if (url.pathname === "/api/logs/follow") {
      return json({
        events: [
          {
            sequence: 9,
            timestamp: "2026-10-01T08:00:00Z",
            level: "info",
            component: "runtime",
            name: "runtime.long_payload",
            message: "Long payload fixture",
            workspace_id: "ws_fixture",
            payload: {
              rows: Array.from({ length: 60 }, (_, index) => ({
                index,
                value: `row-${index}`,
              })),
              long_token: longToken,
            },
          },
        ],
        total: 1,
        truncated: false,
      })
    }
    if (url.pathname === "/api/logs/info") {
      return json({ path: "/tmp/codemcp/runtime.jsonl", files: 2, bytes: 4096 })
    }
    if (url.pathname === "/api/logs" && request.method() === "DELETE") {
      return json({ cleared: true })
    }
    if (url.pathname === "/api/tools") {
      const fixture = {
        name: "quality_fixture_tool",
        title: "Quality fixture tool",
        description:
          "A deliberately long schema fixture used to verify detail scrolling and keyboard navigation.",
        annotations: { readOnlyHint: false, destructiveHint: true },
        inputSchema: {
          type: "object",
          properties: Object.fromEntries(
            Array.from({ length: 50 }, (_, index) => [
              `property_${index}`,
              {
                type: "string",
                description: `${longToken}-${index}`,
              },
            ])
          ),
        },
        outputSchema: {
          type: "object",
          properties: {
            output: { type: "string", description: longToken },
          },
        },
      }
      return json([
        fixture,
        ...Array.from({ length: 24 }, (_, index) => ({
          ...fixture,
          name: `fixture_tool_${String(index + 1).padStart(2, "0")}`,
          title: `Fixture tool ${index + 1}`,
          description: `Pagination fixture ${index + 1}`,
          inputSchema: { type: "object", properties: {} },
        })),
      ])
    }
    if (url.pathname === "/api/activity/stream") {
      activityCalls++
      if (options.activityReconnect && activityCalls === 1) {
        await new Promise((resolve) => setTimeout(resolve, 250))
        return route.fulfill({
          status: 503,
          contentType: "text/plain",
          body: "temporary stream failure",
        })
      }
      return route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: 'event: ready\ndata: {"latest_sequence":0}\n\n',
      })
    }
    const workspaceFixture = {
      id: "ws_fixture",
      path: `/workspace/${longToken}`,
      allow_dirs: [`/workspace/extra/${longToken}`],
      available: true,
    }
    const executionFixture = {
      id: `exec_${longToken}`,
      workspace_id: "ws_fixture",
      tool: "run_command",
      command: `printf-${longToken}`,
      cwd: `/workspace/${longToken}`,
      started_at: "2026-10-01T08:00:00Z",
      finished_at: "2026-10-01T08:00:01Z",
      status: "success",
      source: "mcp",
      exit_code: 0,
    }
    if (url.pathname === "/api/workspaces") return json([workspaceFixture])
    if (url.pathname === "/api/workspaces/ws_fixture")
      return json(workspaceFixture)
    if (url.pathname === "/api/workspaces/ws_fixture/context") {
      const contextText = `# Fixture context\n\n${longToken}\n${longToken}`
      return json({
        root: workspaceFixture.path,
        workspace_id: workspaceFixture.id,
        summary: {
          memory_files: [],
          memory_bytes: contextText.length,
          instruction_bytes: contextText.length,
          git: { is_repo: true, branch: "main", commits: 12 },
          rules: 1,
          skills: 1,
        },
        instruction_context: {
          root: workspaceFixture.path,
          workspace_id: workspaceFixture.id,
          instructions_text: contextText,
          instruction_bytes: contextText.length,
          instruction_truncated: false,
          rules: [
            {
              path: `.cm/rules/${longToken}.md`,
              source: ".cm",
              content: contextText,
              always_apply: true,
            },
          ],
          skills: [
            {
              name: "fixture-skill",
              description: longToken,
              path: `.cm/skills/${longToken}/SKILL.md`,
              source: ".cm",
            },
          ],
          sources: [],
          project_memory: {
            sections: [],
            imports: [],
            total_bytes: 0,
            budget_bytes: 65536,
            budget_truncated: false,
          },
          auto_memory: { loaded: false, bytes: 0 },
          git: {
            is_repo: true,
            root: workspaceFixture.path,
            branch: "main",
            status_short: "",
            recent_commits: ["fixture commit"],
          },
          environment: { fixture: longToken },
        },
      })
    }
    if (url.pathname === "/api/workspaces/ws_fixture/executions/stream") {
      return route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: `event: ready\ndata: ${JSON.stringify({
          executions: [executionFixture],
          events: [],
          latest_sequence: 1,
          replay_count: 0,
        })}\n\n`,
      })
    }
    if (
      url.pathname ===
      `/api/workspaces/ws_fixture/executions/${encodeURIComponent(executionFixture.id)}`
    ) {
      return json({
        execution: executionFixture,
        stdout: `stdout-${longToken}\n${longToken}`,
        stderr: `stderr-${longToken}`,
        latest_sequence: 1,
      })
    }
    if (url.pathname === "/api/workspaces/ws_fixture/processes") {
      return json([
        {
          id: `proc_${longToken}`,
          pid: 4242,
          command: `background-${longToken}`,
          cwd: `/workspace/process/${longToken}`,
          started_at: "2026-10-01T08:00:00Z",
          running: false,
          exit_code: 0,
        },
      ])
    }
    if (url.pathname === "/api/workspaces/ws_fixture/integrations/codegraph") {
      return json({
        enabled: true,
        indexed: true,
        workspace_id: "ws_fixture",
        index_path: `/index/${longToken}`,
      })
    }
    if (url.pathname === "/api/requests") return json([])
    if (url.pathname === "/api/requests/grants") return json([])
    if (url.pathname === "/api/upstream") return json([])
    if (url.pathname === "/api/tunnel") {
      return json({
        provider: "openai",
        enabled: true,
        running: true,
        ready: true,
        metadata: { name: "Fixture tunnel" },
      })
    }
    if (url.pathname === "/api/tunnel/config") return json({ enabled: true })
    if (url.pathname === "/api/network/interfaces") return json([])
    if (url.pathname === "/api/auth") {
      return json({
        mcp_enabled: true,
        mcp_configured: true,
        mcp_legacy_bearer: false,
        admin_enabled: true,
        admin_configured: true,
        unauthenticated_loopback: false,
        cleartext_http: false,
      })
    }
    if (url.pathname === "/api/settings") return json([])
    if (url.pathname === "/api/notifications") return json({})
    if (url.pathname === "/api/config") {
      return json({
        http: {
          exposure: { mode: "none", interfaces: [] },
          security: {
            allow_insecure: false,
            allow_unauthenticated_loopback: false,
          },
          mcp: {
            enabled: true,
            port: 37421,
            auth: {
              enabled: true,
              legacy_bearer: true,
              token_configured: true,
            },
          },
          admin: {
            enabled: true,
            port: 37422,
            auth: { enabled: true, token_configured: true },
          },
        },
        permissions: { allow_dirs: [] },
        shell: { path: [] },
        integrations: {
          ponytail: { active: true, mode: "full" },
          caveman: { active: true, mode: "full" },
          rtk: { enabled: true, path: "" },
          codegraph: { enabled: false, path: "" },
          typesafe: {
            enabled: false,
            model: "fixture-model",
            timeout_ms: 5000,
          },
        },
      })
    }
    return json({})
  })

  return {
    activityCalls: () =>
      page.evaluate(
        () =>
          (
            window as typeof window & {
              __activityStreamCalls?: number
            }
          ).__activityStreamCalls ?? 0
      ),
  }
}

export async function installMiniAppMocks(page: Page) {
  await page.route("https://telegram.org/js/telegram-web-app.js*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/javascript",
      body: "/* Telegram SDK replaced by the deterministic browser fixture. */",
    })
  )
  await page.addInitScript(() => {
    const callbacks = new Map<string, Set<(...args: unknown[]) => void>>()
    const button = () => {
      let click: (() => void) | null = null
      const value = {
        isVisible: false,
        isActive: true,
        setText() {
          return value
        },
        show() {
          value.isVisible = true
          return value
        },
        hide() {
          value.isVisible = false
          return value
        },
        enable() {
          value.isActive = true
          return value
        },
        disable() {
          value.isActive = false
          return value
        },
        showProgress() {
          return value
        },
        hideProgress() {
          return value
        },
        setParams() {
          return value
        },
        onClick(callback: () => void) {
          click = callback
          return value
        },
        offClick(callback: () => void) {
          if (click === callback) click = null
          return value
        },
        trigger() {
          click?.()
        },
      }
      return value
    }
    const backButton = button()

    window.Telegram = {
      WebApp: {
        initData: "signed-browser-fixture",
        isActive: true,
        colorScheme: "light",
        get viewportHeight() {
          return window.innerHeight
        },
        get viewportStableHeight() {
          return window.innerHeight
        },
        safeAreaInset: { top: 0, right: 0, bottom: 0, left: 0 },
        contentSafeAreaInset: { top: 0, right: 0, bottom: 0, left: 0 },
        ready() {},
        expand() {},
        enableVerticalSwipes() {},
        disableVerticalSwipes() {},
        showConfirm(_message, callback) {
          callback?.(true)
        },
        onEvent(event, callback) {
          const values = callbacks.get(event) ?? new Set()
          values.add(callback)
          callbacks.set(event, values)
        },
        offEvent(event, callback) {
          callbacks.get(event)?.delete(callback)
        },
        BackButton: backButton,
        MainButton: button(),
        SecondaryButton: button(),
        SettingsButton: {
          show() {
            return this
          },
          hide() {
            return this
          },
          onClick() {
            return this
          },
          offClick() {
            return this
          },
        },
        HapticFeedback: { impactOccurred() {} },
      },
    }

    type Listener = ((event: MessageEvent<string>) => void) | null
    class FixtureWebSocket {
      static instances: FixtureWebSocket[] = []
      url: string
      readyState = 1
      onmessage: Listener = null
      onerror: (() => void) | null = null
      onclose: (() => void) | null = null

      constructor(url: string | URL) {
        this.url = String(url)
        FixtureWebSocket.instances.push(this)
        queueMicrotask(() => this.snapshot())
      }

      snapshot() {
        const feed = new URL(this.url).searchParams.get("feed") || "runtime"
        const payload =
          feed === "runtime"
            ? {
                events: Array.from({ length: 30 }, (_, index) => ({
                  sequence: index + 1,
                  timestamp: "2026-10-01T08:00:00Z",
                  level: index === 0 ? "error" : "info",
                  component: "runtime",
                  event:
                    index === 0 ? "runtime.failure" : `runtime.event.${index}`,
                  message:
                    index === 0 ? "Failure fixture" : `Runtime event ${index}`,
                  workspace_id: "ws_fixture",
                  fields: [
                    {
                      key: "long",
                      value: "mini-segment-".repeat(60),
                    },
                  ],
                })),
                total: 30,
                truncated: false,
                latest_sequence: 30,
              }
            : feed === "executions"
              ? {
                  executions: [
                    {
                      id: "exec_fixture",
                      workspace_id: "ws_fixture",
                      tool: "run_command",
                      command: "printf fixture",
                      cwd: "/workspace",
                      started_at: "2026-10-01T08:00:00Z",
                      status: "success",
                      source: "mcp",
                      exit_code: 0,
                    },
                  ],
                  events: [
                    {
                      sequence: 31,
                      execution_id: "exec_fixture",
                      workspace_id: "ws_fixture",
                      type: "completed",
                      status: "success",
                    },
                  ],
                  latest_sequence: 31,
                }
              : {
                  records: [
                    {
                      call_id: "call_fixture",
                      first: {
                        sequence: 32,
                        call_id: "call_fixture",
                        kind: "tool_call",
                        phase: "start",
                        method: "tools/call",
                        source: "mcp",
                        tool: "quality_fixture_tool",
                        workspace_id: "ws_fixture",
                        status: "running",
                        timestamp: "2026-10-01T08:00:00Z",
                      },
                      latest: {
                        sequence: 33,
                        call_id: "call_fixture",
                        kind: "tool_call",
                        phase: "finish",
                        method: "tools/call",
                        source: "mcp",
                        tool: "quality_fixture_tool",
                        workspace_id: "ws_fixture",
                        status: "ok",
                        duration_ms: 12,
                        timestamp: "2026-10-01T08:00:01Z",
                      },
                    },
                  ],
                  events: [],
                  latest_sequence: 33,
                }
        this.onmessage?.(
          new MessageEvent("message", {
            data: JSON.stringify({
              type: "snapshot",
              feed,
              latest_sequence: payload.latest_sequence,
              payload,
            }),
          })
        )
      }

      send() {}

      close() {
        if (this.readyState === 3) return
        this.readyState = 3
        queueMicrotask(() => this.onclose?.())
      }
    }

    Object.defineProperty(window, "WebSocket", {
      configurable: true,
      value: FixtureWebSocket,
    })
    Object.assign(window, {
      __closeMiniSocket() {
        FixtureWebSocket.instances.at(-1)?.close()
      },
      __telegramBack() {
        backButton.trigger()
      },
    })
  })

  await page.route("**/api/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    if (url.pathname === "/api/auth") {
      return route.fulfill({ status: 204, body: "" })
    }
    if (url.pathname === "/api/executions/exec_fixture") {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          execution: {
            id: "exec_fixture",
            workspace_id: "ws_fixture",
            tool: "run_command",
            command: "printf fixture",
            requested_command: "printf fixture",
            effective_command: "printf fixture",
            cwd: "/workspace",
            started_at: "2026-10-01T08:00:00Z",
            finished_at: "2026-10-01T08:00:01Z",
            status: "success",
            source: "mcp",
            exit_code: 0,
          },
          stdout: "fixture output\n".repeat(80),
          stderr: "",
          latest_sequence: 31,
        }),
      })
    }
    if (url.pathname === "/api/tool-calls/call_fixture") {
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          sequence: 33,
          call_id: "call_fixture",
          kind: "tool_call",
          phase: "finish",
          method: "tools/call",
          source: "mcp",
          tool: "quality_fixture_tool",
          workspace_id: "ws_fixture",
          status: "ok",
          duration_ms: 12,
          timestamp: "2026-10-01T08:00:01Z",
          request: {
            tool: "quality_fixture_tool",
            arguments: { input: "mini-request-".repeat(50) },
          },
          response: {
            output: "mini-response-".repeat(50),
            rows: Array.from({ length: 80 }, (_, index) => index),
          },
          diagnostic: { redacted: false, truncated: false },
        }),
      })
    }
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: "{}",
    })
  })
}

export async function expectNoViewportOverflow(page: Page) {
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          document.documentElement.scrollWidth <= window.innerWidth + 1 &&
          document.body.scrollWidth <= window.innerWidth + 1
      )
    )
    .toBe(true)
}

export async function expectBothAxisOverflow(scrollArea: Locator) {
  const viewport = scrollArea.locator(
    ':scope > [data-slot="scroll-area-viewport"]'
  )
  await expect(viewport).toBeVisible()
  await expect
    .poll(() =>
      viewport.evaluate(
        (element) =>
          element.scrollHeight > element.clientHeight + 1 &&
          element.scrollWidth > element.clientWidth + 1
      )
    )
    .toBe(true)
}

export async function expectHorizontalOverflow(scrollArea: Locator) {
  const viewport = scrollArea.locator(
    ':scope > [data-slot="scroll-area-viewport"]'
  )
  await expect(viewport).toBeVisible()
  await expect
    .poll(() =>
      viewport.evaluate(
        (element) => element.scrollWidth > element.clientWidth + 1
      )
    )
    .toBe(true)
}

export async function expectVerticalOverflow(target: Locator) {
  await expect(target).toBeVisible()
  await expect
    .poll(() =>
      target.evaluate(
        (element) => element.scrollHeight > element.clientHeight + 1
      )
    )
    .toBe(true)
}

export async function tabTo(page: Page, target: Locator, limit = 80) {
  await page.locator("body").click({ position: { x: 1, y: 1 } })
  for (let index = 0; index < limit; index++) {
    await page.keyboard.press("Tab")
    if (await target.evaluate((element) => document.activeElement === element))
      return
  }
  throw new Error("Target was not reachable by keyboard tab navigation")
}
