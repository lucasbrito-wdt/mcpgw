"use client";

import { motion, type HTMLMotionProps } from "motion/react";
import type { ComponentProps } from "react";
import { useSyncExternalStore } from "react";

const subscribe = () => () => {};

/** False during SSR and hydration, true afterwards. */
export function useMounted(): boolean {
  return useSyncExternalStore(subscribe, () => true, () => false);
}

/**
 * `motion.span` that renders as a plain span on the server. motion writes its `animate` values into a
 * style attribute while rendering to HTML, and the CSP (`style-src 'self'`, plan gap G26) blocks it.
 * After mount it is a regular motion.span; with `initial={false}` the swap is not visible.
 */
export function MotionSpan(props: HTMLMotionProps<"span">) {
  const mounted = useMounted();
  if (mounted) return <motion.span {...props} />;
  const { initial, animate, exit, transition, layout, layoutId, style, ...plain } = props;
  void [initial, animate, exit, transition, layout, layoutId, style];
  return <span {...(plain as ComponentProps<"span">)} />;
}
