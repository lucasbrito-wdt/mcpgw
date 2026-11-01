"use client";

import { Ban } from "lucide-react";
import { useRef, useState, type KeyboardEvent } from "react";

import { levelLabel } from "../../lib/format";
import { useLocale } from "../../lib/i18n";
import type { AccessLevel } from "../../lib/types";
import { cn } from "../../lib/utils";

/** The ceiling scale, lowest first. A ceiling grants its level and everything under it. */
export const LEVEL_ORDER: readonly AccessLevel[] = ["none", "read", "write", "destructive"];

export const rankOf = (level: AccessLevel): number => LEVEL_ORDER.indexOf(level);

/** Each step keeps its own risk tone, so a high ceiling reads as green, then amber, then red stacked up. */
const STEP_FILL: Record<Exclude<AccessLevel, "none">, string> = {
  read: "bg-ok",
  write: "bg-warn",
  destructive: "bg-cut",
};

export const LEVEL_TEXT: Record<AccessLevel, string> = {
  none: "text-fg-muted",
  read: "text-ok",
  write: "text-warn",
  destructive: "text-cut",
};

export interface CeilingGaugeProps {
  value: AccessLevel;
  onValueChange: (level: AccessLevel) => void;
  /** Accessible name of the radio group, e.g. "Ceiling of ana@x on github". */
  label: string;
  disabled?: boolean;
}

/**
 * Cumulative ceiling gauge: three steps fill up to the chosen level, each in its own risk tone, plus a
 * "no access" stop at the start. One click sets the level; hovering previews the fill. Keyboard: a
 * radio group (arrows, Home, End).
 */
export function CeilingGauge({ value, onValueChange, label, disabled = false }: CeilingGaugeProps) {
  useLocale(); // re-render on a language switch: levelLabel reads the active locale
  const [preview, setPreview] = useState<AccessLevel | null>(null);
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const shown = preview ?? value;
  const shownRank = rankOf(shown);
  const valueRank = rankOf(value);

  function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    const moves: Record<string, number> = { ArrowRight: valueRank + 1, ArrowUp: valueRank + 1, ArrowLeft: valueRank - 1, ArrowDown: valueRank - 1, Home: 0, End: LEVEL_ORDER.length - 1 };
    const target = moves[event.key];
    if (target === undefined || disabled) return;
    event.preventDefault();
    const next = Math.max(0, Math.min(LEVEL_ORDER.length - 1, target));
    if (next !== valueRank) onValueChange(LEVEL_ORDER[next]);
    refs.current[next]?.focus();
  }

  return (
    <div className="flex items-center gap-3">
      <div
        role="radiogroup"
        aria-label={label}
        aria-disabled={disabled || undefined}
        onKeyDown={onKeyDown}
        onPointerLeave={() => setPreview(null)}
        className={cn("flex items-center gap-1", disabled && "pointer-events-none opacity-60")}
      >
        {LEVEL_ORDER.map((level, index) => {
          const checked = level === value;
          const filled = index > 0 && index <= shownRank;
          const ghost = preview !== null && index > valueRank && index <= shownRank;
          return (
            <button
              key={level}
              ref={(el) => {
                refs.current[index] = el;
              }}
              type="button"
              role="radio"
              aria-checked={checked}
              aria-label={levelLabel(level)}
              tabIndex={checked ? 0 : -1}
              disabled={disabled}
              onPointerEnter={() => setPreview(level)}
              onFocus={() => setPreview(null)}
              onClick={() => {
                setPreview(null);
                if (!checked) onValueChange(level);
              }}
              className={cn(
                "focus-ring group relative grid h-8 place-items-center rounded-control",
                index === 0 ? "w-8" : "w-10",
              )}
            >
              {index === 0 ? (
                <Ban
                  aria-hidden
                  className={cn(
                    "size-3.5 transition-colors duration-150 ease-switch",
                    shownRank === 0 ? "text-fg" : "text-fg-subtle group-hover:text-fg-muted",
                  )}
                />
              ) : (
                <span
                  aria-hidden
                  className={cn(
                    "block h-2.5 w-full rounded-[3px] transition-[background-color,opacity] duration-150 ease-switch motion-reduce:transition-none",
                    filled ? STEP_FILL[level as Exclude<AccessLevel, "none">] : "bg-white/8 group-hover:bg-white/14",
                    ghost && "opacity-45",
                  )}
                />
              )}
            </button>
          );
        })}
      </div>
      <span aria-hidden className={cn("w-24 truncate text-[13px] font-medium transition-colors duration-150 ease-switch", LEVEL_TEXT[shown], preview !== null && preview !== value && "opacity-70")}>
        {levelLabel(shown)}
      </span>
    </div>
  );
}

/**
 * The account's access silhouette: one bar per server, in the same order on every account, so two
 * people can be compared at a glance. Bar height is the ceiling; no access is a flat dot.
 */
export function CeilingSkyline({ levels }: { levels: readonly AccessLevel[] }) {
  return (
    <span aria-hidden className="flex h-4 items-end gap-[3px]">
      {levels.map((level, index) => {
        const rank = rankOf(level);
        return (
          <span
            key={index}
            className={cn(
              "block w-1 origin-bottom rounded-[1px] transition-[height,background-color] duration-300 ease-switch motion-reduce:transition-none",
              rank === 0 ? "bg-fg-subtle/50" : level === "read" ? "bg-ok" : level === "write" ? "bg-warn" : "bg-cut",
            )}
            style={{ height: `${rank === 0 ? 2 : 4 + rank * 4}px` }}
          />
        );
      })}
    </span>
  );
}
