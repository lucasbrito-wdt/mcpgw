// Ported from design/v5/src/components/ui/segmented-tabs.tsx on 2026-10-31.
"use client";

import { motion, useReducedMotion } from "motion/react";
import { useId, useRef, type KeyboardEvent } from "react";

import { SWITCH_TRANSITION } from "../../lib/motion";
import { cn } from "../../lib/utils";

export interface SegmentedTabsItem<T extends string> {
  value: T;
  label: string;
  /** Shown after the label in mono; keep it equal to what the panel lists. */
  count?: number;
  disabled?: boolean;
}

/** DOM id of one tab, so a panel can point back with aria-labelledby. `id` is the SegmentedTabs `id` prop. */
export function segmentedTabId(id: string, value: string): string {
  return `${id}-tab-${value}`;
}

export interface SegmentedTabsProps<T extends string> {
  items: readonly SegmentedTabsItem<T>[];
  value: T;
  onValueChange: (value: T) => void;
  /** Accessible name of the tablist. */
  label: string;
  /** id of the panel the tabs control (aria-controls on every tab). */
  controls?: string;
  /** Base for tab ids (see segmentedTabId). Generated when omitted. */
  id?: string;
  /** "fill" stretches segments to the container width (mobile toggles like OAuth | Token). */
  fill?: boolean;
  size?: "sm" | "md";
  className?: string;
}

/**
 * Airbyte segmented toggle: active segment gets signal-wash and a signal border that slides between segments (layoutId).
 * Keyboard: arrows, Home and End move and select (roving tabindex).
 */
export function SegmentedTabs<T extends string>({
  items,
  value,
  onValueChange,
  label,
  controls,
  id,
  fill = false,
  size = "md",
  className,
}: SegmentedTabsProps<T>) {
  const generatedId = useId();
  const baseId = id ?? generatedId;
  const reduceMotion = useReducedMotion();
  const listRef = useRef<HTMLDivElement>(null);
  const enabled = items.filter((item) => !item.disabled);

  function select(next: T) {
    onValueChange(next);
    listRef.current?.querySelector<HTMLButtonElement>(`[data-value="${CSS.escape(next)}"]`)?.focus();
  }

  function onKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    const index = enabled.findIndex((item) => item.value === value);
    const last = enabled.length - 1;
    const moves: Record<string, number> = {
      ArrowRight: index === last ? 0 : index + 1,
      ArrowLeft: index === 0 ? last : index - 1,
      Home: 0,
      End: last,
    };
    if (!(event.key in moves)) return;
    event.preventDefault();
    select(enabled[moves[event.key]].value);
  }

  return (
    <div
      ref={listRef}
      role="tablist"
      aria-label={label}
      onKeyDown={onKeyDown}
      className={cn(
        "scrollbar-none inline-flex w-fit max-w-full items-stretch gap-0.5 overflow-x-auto rounded-control border border-line-strong bg-ink/60 p-0.5",
        fill && "flex w-full",
        className,
      )}
    >
      {items.map((item) => {
        const isActive = item.value === value;
        return (
          <button
            key={item.value}
            type="button"
            role="tab"
            id={segmentedTabId(baseId, item.value)}
            data-value={item.value}
            aria-selected={isActive}
            aria-controls={controls}
            tabIndex={isActive ? 0 : -1}
            disabled={item.disabled}
            onClick={() => onValueChange(item.value)}
            className={cn(
              "relative inline-flex shrink-0 cursor-pointer items-center justify-center gap-2 whitespace-nowrap rounded-[5px] font-semibold outline-none transition-colors duration-150 ease-switch focus-visible:ring-2 focus-visible:ring-signal-fg disabled:cursor-not-allowed disabled:opacity-45",
              size === "sm" ? "h-7 px-2.5 text-xs" : "h-8 px-3 text-[13px]",
              fill && "flex-1",
              isActive ? "text-signal-fg" : "text-fg-muted hover:text-fg active:text-fg",
            )}
          >
            {isActive ? (
              <motion.span
                layoutId={`segment-${baseId}`}
                transition={reduceMotion ? { duration: 0 } : SWITCH_TRANSITION}
                className="absolute inset-0 rounded-[5px] border border-signal bg-signal-wash"
                aria-hidden
              />
            ) : null}
            <span className="relative">{item.label}</span>
            {item.count !== undefined ? (
              <span className={cn("relative font-mono text-[11px] tabular-nums", isActive ? "text-signal-fg" : "text-fg-muted")}>
                {item.count}
              </span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}
