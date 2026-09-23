import { describe, expect, it } from "vitest"
import { analyzeMCPServerJSON } from "@/lib/mcp-server-json"

describe("MCP server JSON parser", () => {
  it("parses a raw server object and infers stdio", () => {
    const result = analyzeMCPServerJSON(JSON.stringify({ id: "local", command: "node", args: ["./server.js"] }))
    expect(result.error).toBeUndefined()
    expect(result.kind).toBe("single")
    expect(result.items[0].errors).toEqual([])
    expect(result.items[0].server).toMatchObject({ id: "local", name: "local", transport: "stdio", command: "node", enabled: true, expose: "all", auth: { type: "none" } })
  })

  it("imports an mcpServers map and infers HTTP or stdio per entry", () => {
    const result = analyzeMCPServerJSON(JSON.stringify({ mcpServers: { local: { command: "node", args: ["./server.js"], disabled: true }, docs: { type: "streamable-http", url: "https://example.com/mcp", headers: { Authorization: "Bearer token" } } } }))
    expect(result.kind).toBe("collection")
    expect(result.items).toHaveLength(2)
    expect(result.items[0].server).toMatchObject({ id: "local", transport: "stdio", enabled: false })
    expect(result.items[1].server).toMatchObject({ id: "docs", transport: "http", url: "https://example.com/mcp", enabled: true })
  })

  it("keeps valid entries when another collection entry is invalid", () => {
    const result = analyzeMCPServerJSON(JSON.stringify({ mcpServers: { good: { command: "node" }, bad: { transport: "stdio" } } }))
    expect(result.items.find((item) => item.key === "good")?.errors).toEqual([])
    expect(result.items.find((item) => item.key === "bad")?.errors).toContain("stdio server requires command.")
  })

  it("rejects duplicate IDs in arrays and malformed JSON", () => {
    const duplicates = analyzeMCPServerJSON(JSON.stringify([{ id: "same", command: "node" }, { id: "same", command: "node" }]))
    expect(duplicates.items.every((item) => item.errors.some((error) => error.includes("duplicate server id")))).toBe(true)
    expect(analyzeMCPServerJSON("{").error).toBeTruthy()
  })
})
