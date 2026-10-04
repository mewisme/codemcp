import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"

import { semanticStatusTone } from "@/components/semantic-status"
import { SemanticStatusBadge } from "@/components/semantic-status-badge"

describe("semantic status presentation", () => {
  it("maps status vocabulary to one shared tone authority", () => {
    expect(semanticStatusTone("failed")).toBe("danger")
    expect(semanticStatusTone("ok")).toBe("success")
    expect(semanticStatusTone("running")).toBe("active")
    expect(semanticStatusTone("warning")).toBe("warning")
    expect(semanticStatusTone("unknown")).toBe("neutral")
  })

  it("renders success and warning tones without introducing surface-specific variants", () => {
    const { rerender } = render(<SemanticStatusBadge status="success" />)
    expect(screen.getByText("success")).toHaveClass("bg-emerald-500/10")

    rerender(<SemanticStatusBadge status="warning" />)
    expect(screen.getByText("warning")).toHaveClass("bg-amber-500/10")
  })
})
