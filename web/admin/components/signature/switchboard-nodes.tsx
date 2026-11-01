// Ported from design/v5/src/components/signature/switchboard-nodes.tsx on 2026-10-31.
"use client";

import { KeyRound, LockOpen, ShieldAlert, TimerOff, Unplug, Users, type LucideIcon } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import Link from "next/link";
import { useEffect, useState } from "react";

import { McpgwTile } from "../brand/McpgwMark";
import { ServiceLogo } from "../brand/ServiceLogo";
import { MonoLabel } from "../ui/mono-label";
import { MotionSpan } from "../ui/motion-span";
import { usePrefersReducedMotion } from "../../hooks/usePrefersReducedMotion";
import { KEY_LABEL, PROBLEM_LABEL } from "../../lib/format";
import { useLocale, useT } from "../../lib/i18n";
import { signature } from "../../lib/i18n/messages/signature";
import { DURATION, EASE_SWITCH } from "../../lib/motion";
import type { Agent, ConnectionProblem, KeyKind, SwitchboardServer } from "../../lib/types";
import { cn } from "../../lib/utils";

/** What the key node shows for one server: the key a call resolves to, or why the viewer's key is broken. */
export type KeyState = KeyKind | ConnectionProblem;

export function keyStateOf(server: SwitchboardServer, override?: KeyKind): KeyState {
  return override ?? server.problem ?? server.key;
}

/**
 * How a key state lights the route. "a" is core to key, "b" is key to server.
 * lit = solid wire in that color, broken = dashed wire (the call does not get through), off = untouched.
 */
export interface KeyStyle {
  label: string;
  icon: LucideIcon;
  text: string;
  color: string;
  /** Box glow of the key node (static class: no style attribute reaches the HTML). */
  glow: string;
  /** Drop-shadow filter of the lit route. */
  drop: string;
  a: "lit" | "broken";
  b: "lit" | "broken";
}

/** `label` is a getter: it reads the active locale when used, not when the module loads. */
export const KEY_STYLE: Record<KeyState, KeyStyle> = {
  personal: { get label() { return KEY_LABEL.personal; }, icon: KeyRound, text: "text-core", color: "var(--color-core)", glow: "shadow-[0_0_32px_-8px_var(--color-core)]", drop: "drop-shadow-[0_0_4px_var(--color-core)]", a: "lit", b: "lit" },
  shared: { get label() { return KEY_LABEL.shared; }, icon: Users, text: "text-signal-fg", color: "var(--color-signal-fg)", glow: "shadow-[0_0_32px_-8px_var(--color-signal-fg)]", drop: "drop-shadow-[0_0_4px_var(--color-signal-fg)]", a: "lit", b: "lit" },
  open: { get label() { return KEY_LABEL.open; }, icon: LockOpen, text: "text-ok", color: "var(--color-ok)", glow: "shadow-[0_0_32px_-8px_var(--color-ok)]", drop: "drop-shadow-[0_0_4px_var(--color-ok)]", a: "lit", b: "lit" },
  none: { get label() { return KEY_LABEL.none; }, icon: Unplug, text: "text-fg-muted", color: "var(--color-fg-subtle)", glow: "shadow-[0_0_32px_-8px_var(--color-fg-subtle)]", drop: "drop-shadow-[0_0_4px_var(--color-fg-subtle)]", a: "lit", b: "broken" },
  revoked: { get label() { return PROBLEM_LABEL.revoked; }, icon: ShieldAlert, text: "text-cut", color: "var(--color-cut)", glow: "shadow-[0_0_32px_-8px_var(--color-cut)]", drop: "drop-shadow-[0_0_4px_var(--color-cut)]", a: "lit", b: "broken" },
  expired: { get label() { return PROBLEM_LABEL.expired; }, icon: TimerOff, text: "text-warn", color: "var(--color-warn)", glow: "shadow-[0_0_32px_-8px_var(--color-warn)]", drop: "drop-shadow-[0_0_4px_var(--color-warn)]", a: "broken", b: "broken" },
  scopes: { get label() { return PROBLEM_LABEL.scopes; }, icon: ShieldAlert, text: "text-warn", color: "var(--color-warn)", glow: "shadow-[0_0_32px_-8px_var(--color-warn)]", drop: "drop-shadow-[0_0_4px_var(--color-warn)]", a: "lit", b: "broken" },
};

/* ------------------------------------------------------------------------------------------ agents */

export function AgentPill({ agent }: { agent: Agent }) {
  return (
    <li
      data-sb="agent"
      data-id={agent.id}
      className="relative z-10 flex h-11 items-center @min-[50rem]:w-full gap-2.5 rounded-card bg-linear-to-b from-tile to-tile/60 px-3 text-sm font-medium text-fg shadow-[inset_0_1px_0_var(--color-line-strong)] ring-1 ring-line"
    >
      <span className="grid size-6 shrink-0 place-items-center">
        <ServiceLogo slug={agent.id} name={agent.name} variant="glyph" size="sm" />
      </span>
      <span className="truncate">{agent.name}</span>
    </li>
  );
}

/* ------------------------------------------------------------------------------------------ core */

/** The luminous gateway tile. Lights up once during the intro. */
export function CoreNode({ intro }: { intro: boolean }) {
  const [lit, setLit] = useState(false);
  useEffect(() => {
    const frame = requestAnimationFrame(() => setLit(true));
    return () => cancelAnimationFrame(frame);
  }, []);
  const dim = intro && !lit;
  return (
    <div data-sb="core" className="relative z-10 shrink-0">
      <McpgwTile
        markClassName="size-9"
        className={cn(
          "size-20 transition-[opacity,scale,filter] duration-900 delay-450 ease-switch motion-reduce:transition-none @min-[50rem]:size-22",
          dim && "scale-92 opacity-35 brightness-60",
        )}
      />
      <MonoLabel as="p" tone="strong" className="absolute top-full left-1/2 mt-3 -translate-x-1/2 whitespace-nowrap">
        mcpgw
      </MonoLabel>
    </div>
  );
}

/* ------------------------------------------------------------------------------------------ key */

/** The art's "context store" tile, here the key the gateway resolved for the active server. */
export function KeyNode({ state }: { state: KeyState | null }) {
  const t = useT(signature);
  const reduceMotion = usePrefersReducedMotion();
  const style = state ? KEY_STYLE[state] : null;
  const Icon = style?.icon ?? KeyRound;
  const swap = reduceMotion
    ? { initial: { opacity: 0 }, animate: { opacity: 1 }, exit: { opacity: 0 } }
    : { initial: { opacity: 0, y: 6 }, animate: { opacity: 1, y: 0 }, exit: { opacity: 0, y: -6 } };
  return (
    <div data-sb="key" className="relative z-10 shrink-0">
      <div
        className={cn(
          "grid size-17 place-items-center rounded-tile bg-black ring-1 transition-[box-shadow,color] duration-300 ease-switch",
          style ? "ring-line-strong" : "ring-line",
          style?.text ?? "text-signal-fg",
          style?.glow,
        )}
      >
        <AnimatePresence initial={false}>
          <MotionSpan key={state ?? "idle"} {...swap} transition={{ duration: DURATION.state, ease: EASE_SWITCH }} className="col-start-1 row-start-1 grid place-items-center">
            <Icon className="size-7" strokeWidth={1.6} aria-hidden />
          </MotionSpan>
        </AnimatePresence>
      </div>
      <div className="absolute top-full left-1/2 mt-3 grid -translate-x-1/2 justify-items-center whitespace-nowrap">
        <AnimatePresence initial={false}>
          <MotionSpan key={state ?? "idle"} {...swap} transition={{ duration: DURATION.state, ease: EASE_SWITCH }} className="col-start-1 row-start-1">
            <MonoLabel tone="strong" className={style?.text}>
              {style ? style.label : t("keyIdle")}
            </MonoLabel>
          </MotionSpan>
        </AnimatePresence>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------------------------------ servers */

const PROBLEM_DOT: Record<ConnectionProblem, string> = { revoked: "bg-cut", expired: "bg-warn", scopes: "bg-warn" };

export interface ServerTileProps {
  server: SwitchboardServer;
  href: string;
  active: boolean;
  /** Hidden below the row layout (only the first rows show on mobile). */
  wideOnly: boolean;
  onActivate: (slug: string | null) => void;
}

export function ServerTile({ server, href, active, wideOnly, onActivate }: ServerTileProps) {
  useLocale();
  const state = keyStateOf(server);
  const dimmed = state === "none";
  return (
    <li data-sb="tile" data-slug={server.slug} className={cn("relative", wideOnly && "@max-[50rem]:hidden")}>
      <Link
        href={href}
        aria-label={`${server.name}: ${KEY_STYLE[state].label}`}
        onPointerEnter={() => onActivate(server.slug)}
        onPointerLeave={() => onActivate(null)}
        onFocus={() => onActivate(server.slug)}
        onBlur={() => onActivate(null)}
        className={cn(
          "focus-ring group block rounded-tile transition-[translate,opacity] duration-300 ease-switch motion-safe:hover:-translate-y-0.5",
          dimmed && !active && "opacity-60",
        )}
      >
        <ServiceLogo
          slug={server.slug}
          name={server.name}
          size="lg"
          className={cn("transition-shadow duration-300 ease-switch", active && "ring-2 ring-core/80")}
        />
        {server.problem ? (
          <span className={cn("absolute -top-0.5 -right-0.5 size-2.5 rounded-full ring-2 ring-black", PROBLEM_DOT[server.problem])} aria-hidden />
        ) : null}
      </Link>
    </li>
  );
}
