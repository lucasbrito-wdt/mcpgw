// Ported from design/v5/src/components/signature/RouteLine.tsx on 2026-10-31.
import type { CallKey, CallResult, KeyKind, RouteCut } from "../../lib/types";
import { cn } from "../../lib/utils";

import { RouteHero, type RouteHeroProps } from "./route-hero";

export type { RouteHeroProps } from "./route-hero";

/** Key on a route. Accepts the server vocabulary (KeyKind) and the log vocabulary (CallKey). */
export type RouteKey = KeyKind | CallKey;

export interface RouteMiniProps {
  size: "mini";
  /** Key the gateway resolved for this call. */
  keyKind: RouteKey;
  result?: CallResult;
  /** Where the call stopped. Defaults from result: denied stops at the gateway, error upstream. */
  cut?: RouteCut;
  /** When set, the last segment length encodes it on a log scale (1 ms to 10 s). */
  latencyMs?: number;
  /** Accessible summary, e.g. "Recusada no gateway". Omit when the row already says it. */
  label?: string;
  className?: string;
}

export type RouteLineProps = RouteMiniProps | ({ size: "hero" } & RouteHeroProps);

/**
 * The resolution route, agent to key to server. "mini" is the inline glyph for log rows and
 * cards (the cut shows where it failed); "hero" is the server detail header with the key swap.
 */
export function RouteLine(props: RouteLineProps) {
  if (props.size === "hero") return <RouteHero {...props} />;
  return <RouteMini {...props} />;
}

/* ------------------------------------------------------------------------------------------ mini */

const R = 5;
const PAD = R + 2;
const LAT_MIN = 8; // px for 1 ms
const LAT_SPAN = 88; // extra px up to 10 s

/** Last segment length: 8 px at 1 ms, 96 px at 10 s, log scale (4 decades). */
export function latencyLength(ms: number): number {
  const t = Math.min(Math.max(Math.log10(Math.max(ms, 1)) / 4, 0), 1);
  return Math.round(LAT_MIN + t * LAT_SPAN);
}

type MiniKey = "personal" | "shared" | "open" | "none";
const NORMALIZE: Record<RouteKey, MiniKey> = { personal: "personal", own: "personal", shared: "shared", open: "open", none: "none", missing: "none" };

const STROKE = {
  wire: "var(--color-fg-muted)",
  own: "var(--color-core)",
  idle: "var(--color-fg-subtle)",
  cut: "var(--color-cut)",
  warn: "var(--color-warn)",
} as const;

function Line({ x1, x2, tone = "wire" }: { x1: number; x2: number; tone?: "wire" | "own" | "idle" }) {
  if (x2 - x1 < 1) return null;
  return <line x1={x1} y1={PAD} x2={x2} y2={PAD} stroke={STROKE[tone]} strokeWidth={1.5} strokeDasharray={tone === "idle" ? "2 3" : undefined} />;
}

function CutMark({ x, tone }: { x: number; tone: "cut" | "warn" }) {
  return <path d={`M${x - 3} ${PAD - 4}l2 8M${x + 1} ${PAD - 4}l2 8`} stroke={STROKE[tone]} strokeWidth={1.75} />;
}

function KeyDot({ x, kind }: { x: number; kind: MiniKey | "strain" }) {
  if (kind === "personal") return <circle cx={x} cy={PAD} r={R} fill="var(--color-core)" />;
  if (kind === "shared") return <circle cx={x} cy={PAD} r={R - 1} fill="var(--color-panel)" stroke="var(--color-signal-fg)" strokeWidth={2} />;
  if (kind === "open") return <circle cx={x} cy={PAD} r={R / 2} fill={STROKE.wire} />;
  if (kind === "strain") return <circle cx={x} cy={PAD} r={R - 1} fill="var(--color-panel)" stroke={STROKE.warn} strokeWidth={2} strokeDasharray="3 2" />;
  return <circle cx={x} cy={PAD} r={R - 0.6} fill="none" stroke={STROKE.idle} strokeWidth={1.25} strokeDasharray="2 2" />;
}

function ServerSquare({ x, state }: { x: number; state: "ok" | "reached" | "hit" | "ghost" }) {
  const common = { x: x - R, y: PAD - R, width: R * 2, height: R * 2, rx: 1.5 };
  if (state === "ok") return <rect {...common} fill="var(--color-fg)" />;
  if (state === "reached") return <rect {...common} fill={STROKE.wire} />;
  if (state === "hit") return <rect {...common} fill="none" stroke={STROKE.cut} strokeWidth={1.5} />;
  return <rect {...common} fill="none" stroke={STROKE.idle} strokeWidth={1.25} />;
}

function RouteMini({ keyKind, result = "ok", cut, latencyMs, label, className }: RouteMiniProps) {
  const key = NORMALIZE[keyKind];
  const stop = cut ?? (result === "denied" ? "gateway" : result === "error" ? "upstream" : undefined);
  const l1 = 32;
  const l2 = latencyMs != null ? latencyLength(latencyMs) : 44;
  const x0 = PAD;
  const x1 = x0 + l1;
  const x2 = x1 + l2;
  const width = x2 + PAD;
  const mid1 = (x0 + x1) / 2;
  const mid2 = (x1 + x2) / 2;
  const wire = key === "personal" ? "own" : "wire";
  const a11y = label ? { role: "img" as const, "aria-label": label } : { "aria-hidden": true as const };

  let body;
  if (stop === "gateway") {
    body = (
      <>
        <Line x1={x0 + R} x2={mid1 - 4} />
        <CutMark x={mid1} tone="cut" />
        <Line x1={mid1 + 5} x2={x1 - R} tone="idle" />
        <circle cx={x1} cy={PAD} r={R - 0.6} fill="none" stroke={STROKE.idle} strokeWidth={1.25} />
        <Line x1={x1 + R + 2} x2={x2 - R - 2} tone="idle" />
        <ServerSquare x={x2} state="ghost" />
      </>
    );
  } else if (key === "none" || stop === "key") {
    body = (
      <>
        <Line x1={x0 + R} x2={x1 - R - 1} />
        <KeyDot x={x1} kind={stop === "key" ? "strain" : "none"} />
        <Line x1={x1 + R + 2} x2={x2 - R - 2} tone="idle" />
        <ServerSquare x={x2} state="ghost" />
      </>
    );
  } else if (stop === "scopes") {
    body = (
      <>
        <Line x1={x0 + R} x2={x1 - R - 1} tone={wire} />
        <KeyDot x={x1} kind={key} />
        <Line x1={x1 + R + 1} x2={mid2 - 4} tone={wire} />
        <CutMark x={mid2} tone="warn" />
        <Line x1={mid2 + 5} x2={x2 - R - 2} tone="idle" />
        <ServerSquare x={x2} state="reached" />
      </>
    );
  } else if (stop === "provider" || stop === "upstream") {
    body = (
      <>
        <Line x1={x0 + R} x2={x1 - R - 1} tone={wire} />
        <KeyDot x={x1} kind={key} />
        <Line x1={x1 + R + 1} x2={x2 - R - 7} tone={wire} />
        <CutMark x={x2 - R - 4} tone="cut" />
        <ServerSquare x={x2} state="hit" />
      </>
    );
  } else {
    body = (
      <>
        <Line x1={x0 + R} x2={x1 - R - 1} tone={wire} />
        <KeyDot x={x1} kind={key} />
        <Line x1={x1 + R + 1} x2={x2 - R - 1} tone={wire} />
        <ServerSquare x={x2} state="ok" />
      </>
    );
  }

  return (
    <svg
      viewBox={`0 0 ${width} ${PAD * 2}`}
      width={width}
      height={PAD * 2}
      fill="none"
      strokeLinecap="round"
      className={cn("block shrink-0 overflow-visible", className)}
      {...a11y}
    >
      <circle cx={x0} cy={PAD} r={R * 0.75} fill="var(--color-fg)" />
      {body}
    </svg>
  );
}
