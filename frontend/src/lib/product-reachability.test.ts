import type { RouteObject } from "react-router-dom"
import { describe, expect, it } from "vitest"

import { browserOperationBindings } from "@/lib/operations"
import {
  browserConfirmationEvidence,
  browserDestructiveOperations,
  browserProductReachability,
  browserSurface,
} from "@/lib/product-reachability"
import { adminRoutes } from "@/router"

const browserPageSources = import.meta.glob("../pages/*.tsx", {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>

describe("browser product reachability", () => {
  it("classifies every live canonical API binding under a concrete product route and component", () => {
    const descriptors = browserProductReachability()
    const described = new Set(descriptors.map((descriptor) => descriptor.operation))
    const bound = new Set(
      browserOperationBindings().map((binding) => binding.operation)
    )
    const routes = collectRouteComponents(adminRoutes)

    expect(described).toEqual(bound)
    for (const descriptor of descriptors) {
      expect(descriptor.route.startsWith("/")).toBe(true)
      expect(routes.get(descriptor.route)).toBe(descriptor.component)
      expect(descriptor.action).toBe(descriptor.operation)
      expect(descriptor.api.length).toBeGreaterThan(0)
      expect(browserSurface(descriptor.operation)).toEqual({
        route: descriptor.route,
        component: descriptor.component,
      })
      if (descriptor.state === "gap") {
        expect(descriptor.gap).toBeTruthy()
        expect(descriptor.presentation.unavailable_reason).toBe(descriptor.gap)
      } else {
        expect(descriptor.gap).toBeUndefined()
        expect(descriptor.presentation.unavailable_reason).toBeUndefined()
      }
    }
  })

  it("downgrades destructive flows without production confirmation evidence to gaps", () => {
    const descriptors = new Map(
      browserProductReachability().map((descriptor) => [
        descriptor.operation,
        descriptor,
      ])
    )
    for (const operation of browserDestructiveOperations) {
      const descriptor = descriptors.get(operation)
      if (!descriptor) continue
      expect(descriptor.presentation.danger).toBe("destructive")
      expect(["delete", "review"]).toContain(descriptor.presentation.category)
      const evidence = browserConfirmationEvidence[operation]
      if (!evidence) {
        expect(descriptor.state).toBe("gap")
        expect(descriptor.gap).toContain("confirmation")
        continue
      }
      expect(descriptor.state).toBe("live")
      expect(descriptor.confirmation).toEqual(evidence)
      const source = browserPageSources[evidence.source]
      expect(source, evidence.source).toBeTruthy()
      expect(source).toContain(evidence.marker)
    }
  })

  it("declares protected managed-secret browser inputs", () => {
    const byOperation = new Map(
      browserProductReachability().map((descriptor) => [
        descriptor.operation,
        descriptor,
      ])
    )
    for (const operation of [
      "config.patch",
      "llm.provider.credential.set",
      "tunnel.admin.key.set",
    ]) {
      const descriptor = byOperation.get(operation)
      if (!descriptor) continue
      expect(descriptor.secretPolicy).toBe("protected-input")
      expect(descriptor.presentation.input).toBe("protected-secret")
      expect(descriptor.presentation.secret_policy).toBe("protected-input")
    }
  })
})

function collectRouteComponents(
  routes: RouteObject[],
  parent = ""
): Map<string, string> {
  const result = new Map<string, string>()
  for (const route of routes) {
    const own = route.path ?? ""
    const parentBase = parent.endsWith("/") ? parent.slice(0, -1) : parent
    const absolute = own.startsWith("/")
      ? own
      : [parentBase, own].filter(Boolean).join("/")
    const normalized = normalizeRoute(absolute)
    const handle = route.handle as { component?: string } | undefined
    if (route.path && handle?.component) {
      result.set(normalized, handle.component)
    }
    if (route.children) {
      for (const [path, component] of collectRouteComponents(
        route.children,
        normalized
      )) {
        result.set(path, component)
      }
    }
  }
  return result
}

function normalizeRoute(path: string) {
  const parts = path.split("/").filter(Boolean)
  return parts.length === 0 ? "/" : "/" + parts.join("/")
}
