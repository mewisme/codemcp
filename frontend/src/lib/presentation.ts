import { canonicalPresentationContract } from "@/lib/operation-presentation.generated"

export type CanonicalLifecycleState = keyof typeof canonicalPresentationContract.lifecycle
export type CanonicalNavigationIntent = keyof typeof canonicalPresentationContract.navigation

export function canonicalLifecycleLabel(state: CanonicalLifecycleState) {
  return canonicalPresentationContract.lifecycle[state].label
}

export function canonicalNavigationLabel(intent: CanonicalNavigationIntent) {
  return canonicalPresentationContract.navigation[intent]
}
