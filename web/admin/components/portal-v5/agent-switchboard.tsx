// Ported from design/v5/src/components/portal/agent-switchboard.tsx on 2026-10-31.
// The two `style` heights became classes (G26) and the "live" dots are gone: there is no agent traffic in the contract.
"use client";

import { motion } from "motion/react";

import { McpgwTile } from "../brand/McpgwMark";
import { ServiceLogo } from "../brand/ServiceLogo";
import { MonoLabel } from "../ui/mono-label";
import { usePrefersReducedMotion } from "../../hooks/usePrefersReducedMotion";
import { DURATION, EASE_SWITCH } from "../../lib/motion";
import type { AgentId } from "../../lib/types";
import { cn } from "../../lib/utils";

import { AGENTS } from "./clients";

/* Fixed geometry: five 50px rows (h-[50px] / h-[250px] below), wires converge on the core at the middle row. */
const ROW = 50;
const HEIGHT = ROW * AGENTS.length;
const CORE_Y = HEIGHT / 2;
const WIDTH = 120;
const wire = (index: number) => {
  const y = ROW * index + ROW / 2;
  return `M0 ${y} C60 ${y}, 60 ${CORE_Y}, ${WIDTH} ${CORE_Y}`;
};

export interface AgentSwitchboardProps {
  selected: AgentId;
  className?: string;
}

/**
 * Mini switchboard: the five clients on the left, mcpgw on the right. The selected client draws its wire in
 * core color with a spark running toward mcpgw (the page's one orchestrated moment). Decorative: the tabs
 * carry the same information for assistive tech.
 */
export function AgentSwitchboard({ selected, className }: AgentSwitchboardProps) {
  const reduceMotion = usePrefersReducedMotion();

  return (
    <div aria-hidden className={cn("@container flex h-[250px] items-stretch justify-center", className)}>
      <ul className="flex shrink-0 flex-col @min-[22rem]:min-w-0 @min-[22rem]:flex-1 @min-[22rem]:shrink">
        {AGENTS.map((agent) => {
          const isSelected = agent.id === selected;
          return (
            <li key={agent.id} className="flex h-[50px] items-center">
              <span
                className={cn(
                  "flex w-fit items-center gap-2 rounded-full border py-1 pr-1 pl-1 @min-[22rem]:w-full transition-[border-color,background-color,color] duration-200 ease-switch @min-[22rem]:pr-3",
                  isSelected ? "border-core/70 bg-core/15 text-fg" : "border-line bg-white/[0.02] text-fg-muted",
                )}
              >
                <ServiceLogo slug={agent.id} name={agent.name} variant="tile" size="sm" className="rounded-full" />
                <span className="hidden min-w-0 flex-1 truncate text-xs font-medium @min-[22rem]:inline">{agent.name}</span>
              </span>
            </li>
          );
        })}
      </ul>

      <svg viewBox={`0 0 ${WIDTH} ${HEIGHT}`} width={WIDTH} height={HEIGHT} className="shrink-0" fill="none">
        {AGENTS.map((agent, index) =>
          agent.id === selected ? null : <path key={agent.id} d={wire(index)} strokeWidth={1.25} strokeDasharray="2 5" className="stroke-line-strong" />,
        )}
        {AGENTS.map((agent, index) =>
          agent.id === selected ? (
            <g key={agent.id}>
              <motion.path
                d={wire(index)}
                strokeWidth={2}
                className="stroke-core"
                initial={reduceMotion ? false : { pathLength: 0 }}
                animate={{ pathLength: 1 }}
                transition={{ duration: DURATION.draw * 0.7, ease: EASE_SWITCH }}
              />
              {reduceMotion ? null : (
                <motion.path
                  d={wire(index)}
                  strokeWidth={3}
                  strokeLinecap="round"
                  className="stroke-spark"
                  pathLength={1}
                  strokeDasharray="0.08 0.92"
                  initial={{ strokeDashoffset: 0.08 }}
                  animate={{ strokeDashoffset: -0.92 }}
                  transition={{ duration: 1.6, ease: EASE_SWITCH, repeat: Infinity, repeatDelay: 0.6, delay: DURATION.draw * 0.7 }}
                />
              )}
            </g>
          ) : null,
        )}
      </svg>

      <div className="flex shrink-0 flex-col items-center justify-center gap-2">
        <McpgwTile className="size-14" markClassName="size-7" />
        <MonoLabel tone="strong">mcpgw</MonoLabel>
      </div>
    </div>
  );
}
