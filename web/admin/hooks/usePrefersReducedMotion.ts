// Ported from design/v5/src/hooks/usePrefersReducedMotion.ts on 2026-10-31.
"use client";

import { useSyncExternalStore } from "react";

const QUERY = "(prefers-reduced-motion: reduce)";

function subscribe(onChange: () => void) {
  const media = window.matchMedia(QUERY);
  media.addEventListener("change", onChange);
  return () => media.removeEventListener("change", onChange);
}

/**
 * prefers-reduced-motion that is false during SSR and hydration, then the real value.
 * Use it instead of motion's useReducedMotion whenever the answer changes rendered markup
 * (initial styles, conditional elements); otherwise hydration mismatches for those users.
 */
export function usePrefersReducedMotion(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => window.matchMedia(QUERY).matches,
    () => false,
  );
}
