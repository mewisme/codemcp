import { afterEach, describe, expect, it, vi } from "vitest"

import { api } from "@/lib/api"
import {
  browserOperationFor,
  browserOperationHeaders,
  canonicalOperationHeader,
} from "@/lib/operations"

describe("browser canonical operation adapter", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it("maps concrete resource routes to canonical operations", () => {
    expect(browserOperationFor("GET", "/api/workspaces/ws_test")).toBe("workspace.show")
    expect(browserOperationFor("POST", "/api/workspaces/ws_test/purge")).toBe("workspace.purge")
    expect(browserOperationFor("POST", "/api/workspaces/ws_test/containers")).toBe("workspace.container.add")
    expect(browserOperationFor("GET", "/api/workspaces/ws_test/executions/exec_1/stream")).toBe("execution.stream")
    expect(browserOperationFor("DELETE", "/api/upstream/local/auth/logout")).toBe("upstream.server.auth.logout")
    expect(browserOperationFor("PUT", "/api/tunnel/managed/tun_1")).toBe("tunnel.update")
    expect(browserOperationFor("GET", "/api/completions")).toBe("completion.list")
    expect(browserOperationFor("GET", "/api/completions/current?workspace_id=ws_test")).toBe("completion.current")
    expect(browserOperationFor("GET", "/api/completions/stream?limit=100")).toBe("completion.feed")
    expect(browserOperationFor("GET", "/api/completions/view/completion_1")).toBe("completion.view")
  })

  it("ignores query strings while preserving method semantics", () => {
    expect(browserOperationFor("GET", "/api/requests?status=pending")).toBe("request.list")
    expect(browserOperationFor("POST", "/api/requests?status=pending")).toBeUndefined()
  })

  it("adds only canonical operation metadata to mapped requests", () => {
    const headers = browserOperationHeaders("/api/workspaces/ws_test", "GET", {
      Accept: "application/json",
    })
    expect(headers.get(canonicalOperationHeader)).toBe("workspace.show")
    expect(headers.get("Accept")).toBe("application/json")

    const unknown = browserOperationHeaders("/not-admin", "GET")
    expect(unknown.has(canonicalOperationHeader)).toBe(false)
  })

  it("carries the canonical operation on actual admin API requests", async () => {
    const fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ id: "ws_test", path: "/tmp/project" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    )
    vi.stubGlobal("fetch", fetch)

    await api("/api/workspaces/ws_test")
    expect(fetch).toHaveBeenCalledOnce()
    const [, init] = fetch.mock.calls[0] as [string, RequestInit]
    expect(new Headers(init.headers).get(canonicalOperationHeader)).toBe(
      "workspace.show",
    )
  })
})
