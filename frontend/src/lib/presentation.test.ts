import { describe, expect, it } from "vitest"

import { canonicalLifecycleLabel, canonicalNavigationLabel } from "@/lib/presentation"

describe("canonical presentation vocabulary", () => {
  it("uses one lifecycle vocabulary", () => {
    expect(canonicalLifecycleLabel("working")).toBe("Working")
    expect(canonicalLifecycleLabel("partial")).toBe("Degraded")
    expect(canonicalLifecycleLabel("retryable-failure")).toContain("retry")
    expect(canonicalLifecycleLabel("unavailable")).toBe("Unavailable")
  })

  it("uses one navigation vocabulary", () => {
    expect(canonicalNavigationLabel("back")).toBe("Back")
    expect(canonicalNavigationLabel("home")).toBe("Home")
    expect(canonicalNavigationLabel("refresh")).toBe("Refresh")
    expect(canonicalNavigationLabel("retry")).toBe("Retry")
    expect(canonicalNavigationLabel("cancel")).toBe("Cancel")
    expect(canonicalNavigationLabel("close")).toBe("Close")
  })
})
