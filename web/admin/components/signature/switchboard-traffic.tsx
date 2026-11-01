// Ported from design/v5/src/components/signature/switchboard-traffic.tsx on 2026-10-31.
"use client";

import { animate, motion, useMotionValue } from "motion/react";
import { useEffect, useRef, useState } from "react";

import { EASE_SWITCH } from "../../lib/motion";
import type { AgentId, CallResult, KeyKind, RouteCut } from "../../lib/types";

import { joinPaths, type SwitchboardGeometry } from "./switchboard-geometry";

/** One call travelling the switchboard. */
export interface SwitchboardPacket {
  id: string;
  agent: AgentId;
  /** Server slug. Servers without a visible tile leave through the edge of the grid. */
  server: string;
  result: CallResult;
  /** Where a refused or failed call stopped. Defaults: denied stops at the gateway, error at the upstream. */
  cut?: RouteCut;
  /** Key the call used when it is not the viewer's default for that server (admin traffic of other people). */
  key?: KeyKind;
}

export const PACKET_COLOR: Record<CallResult, string> = {
  ok: "var(--color-spark)",
  denied: "var(--color-cut)",
  error: "var(--color-warn)",
};

const PACKET_GLOW: Record<CallResult, string> = {
  ok: "drop-shadow-[0_0_5px_var(--color-spark)]",
  denied: "drop-shadow-[0_0_5px_var(--color-cut)]",
  error: "drop-shadow-[0_0_5px_var(--color-warn)]",
};

const SPEED = 380; // px per second
const MIN_DURATION = 1.1;

function stopOf(packet: SwitchboardPacket): "core" | "key" | "end" {
  const cut = packet.cut ?? (packet.result === "denied" ? "gateway" : packet.result === "error" ? "upstream" : undefined);
  if (cut === "gateway") return "core";
  if (cut === "key" || cut === "scopes") return "key";
  return "end";
}

interface PacketProps {
  packet: SwitchboardPacket;
  geometry: SwitchboardGeometry;
  /** Fired when the packet passes the key node, so it can show the key this call used. */
  onKey: (packet: SwitchboardPacket) => void;
  onDone: (id: string) => void;
}

/** A dot riding agent wire + core-to-key + tile route, stopping where the call stopped, then a short burst. */
export function Packet({ packet, geometry, onKey, onDone }: PacketProps) {
  const pathRef = useRef<SVGPathElement>(null);
  const x = useMotionValue(-20);
  const y = useMotionValue(-20);
  const [burst, setBurst] = useState<{ x: number; y: number } | null>(null);

  const agentWire = geometry.agentWires[packet.agent] ?? "";
  const tileRoute = geometry.tileRoutes[packet.server] ?? geometry.overflowRoute;
  const full = joinPaths(agentWire, geometry.coreToKey, tileRoute);

  useEffect(() => {
    const path = pathRef.current;
    if (!path) return;
    // Measure each leg on the same element, then restore the full route.
    path.setAttribute("d", agentWire || "M0 0");
    const coreAt = agentWire ? path.getTotalLength() : 0;
    path.setAttribute("d", joinPaths(agentWire, geometry.coreToKey));
    const keyAt = path.getTotalLength();
    path.setAttribute("d", full);
    const total = path.getTotalLength();
    const stop = stopOf(packet);
    const end = stop === "core" ? coreAt : stop === "key" ? keyAt : total;

    let passedKey = false;
    const controls = animate(0, end, {
      duration: Math.max(MIN_DURATION, end / SPEED),
      ease: EASE_SWITCH,
      onUpdate: (length) => {
        const point = path.getPointAtLength(length);
        x.set(point.x);
        y.set(point.y);
        if (!passedKey && length >= keyAt - 1) {
          passedKey = true;
          onKey(packet);
        }
      },
      onComplete: () => {
        const point = path.getPointAtLength(end);
        setBurst({ x: point.x, y: point.y });
      },
    });
    return () => controls.stop();
    // The packet is immutable for its lifetime; geometry changes restart nothing mid-flight.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const color = PACKET_COLOR[packet.result];
  return (
    <g>
      <path ref={pathRef} d={full} fill="none" stroke="none" />
      {burst ? null : (
        <motion.circle cx={x} cy={y} r={3} fill={color} className={PACKET_GLOW[packet.result]} />
      )}
      {burst ? (
        <motion.circle
          cx={burst.x}
          cy={burst.y}
          fill="none"
          stroke={color}
          strokeWidth={1.5}
          initial={{ r: 3, opacity: 1 }}
          animate={{ r: 14, opacity: 0 }}
          transition={{ duration: 0.6, ease: EASE_SWITCH }}
          onAnimationComplete={() => onDone(packet.id)}
        />
      ) : null}
    </g>
  );
}

const MAX_IN_FLIGHT = 6;

/**
 * Decides which packets are in flight.
 * replay: cycles through `traffic` every `everyMs`, forever. live: plays each new id once, as it arrives.
 */
export function useTraffic(traffic: readonly SwitchboardPacket[], mode: "replay" | "live", enabled: boolean, everyMs = 1500) {
  const [flying, setFlying] = useState<SwitchboardPacket[]>([]);
  // Live mode only plays what arrives after mount; the initial list is history, not traffic.
  const [played] = useState(() => new Set(mode === "live" ? traffic.map((packet) => packet.id) : []));
  const cursor = useRef(0);
  const run = useRef(0);

  useEffect(() => {
    if (!enabled || mode !== "replay" || traffic.length === 0) return;
    const timer = window.setInterval(() => {
      if (document.hidden) return;
      const next = traffic[cursor.current % traffic.length];
      cursor.current += 1;
      run.current += 1;
      setFlying((current) => [...current, { ...next, id: `${next.id}#${run.current}` }].slice(-MAX_IN_FLIGHT));
    }, everyMs);
    return () => window.clearInterval(timer);
  }, [enabled, mode, traffic, everyMs]);

  useEffect(() => {
    if (!enabled || mode !== "live") return;
    const fresh = traffic.filter((packet) => !played.has(packet.id));
    if (fresh.length === 0) return;
    for (const packet of fresh) played.add(packet.id);
    // Live traffic arrives from outside; mirroring it into the in-flight list is the point of this effect.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setFlying((current) => [...current, ...fresh].slice(-MAX_IN_FLIGHT));
  }, [enabled, mode, traffic, played]);

  const land = (id: string) => setFlying((current) => current.filter((packet) => packet.id !== id));
  return { flying, land };
}
