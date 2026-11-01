// Ported from design/v5/src/components/ui/chip-ruler.tsx on 2026-10-31.
"use client";

import { ChevronLeft, ChevronRight } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { useT } from "../../lib/i18n";
import { ui } from "../../lib/i18n/messages/ui";
import { cn } from "../../lib/utils";

import { Button } from "./button";

export interface ChipRulerItem<T extends string> {
  value: T;
  label: string;
  count?: number;
}

export interface ChipRulerProps<T extends string> {
  items: readonly ChipRulerItem<T>[];
  value: T;
  onValueChange: (value: T) => void;
  /** Accessible name of the group ("Category"). */
  label: string;
  className?: string;
}

/**
 * One-line chip filter that scrolls sideways. When it overflows, the cut edge fades and an arrow
 * appears on that side, so a label is never clipped without a sign (v4 cut "Sales and paymen").
 */
export function ChipRuler<T extends string>({ items, value, onValueChange, label, className }: ChipRulerProps<T>) {
  const t = useT(ui);
  const scrollerRef = useRef<HTMLDivElement>(null);
  const [edges, setEdges] = useState({ left: false, right: false });

  useEffect(() => {
    const scroller = scrollerRef.current;
    if (!scroller) return;
    const update = () => {
      const max = scroller.scrollWidth - scroller.clientWidth;
      const left = scroller.scrollLeft > 2;
      const right = scroller.scrollLeft < max - 2;
      scroller.style.setProperty("--fade-left", left ? "transparent" : "black");
      scroller.style.setProperty("--fade-right", right ? "transparent" : "black");
      setEdges({ left, right });
    };
    const observer = new ResizeObserver(update);
    observer.observe(scroller);
    scroller.addEventListener("scroll", update, { passive: true });
    return () => {
      observer.disconnect();
      scroller.removeEventListener("scroll", update);
    };
  }, []);

  const scrollBy = (direction: 1 | -1) => {
    const scroller = scrollerRef.current;
    if (!scroller) return;
    const smooth = !window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    scroller.scrollBy({ left: direction * scroller.clientWidth * 0.7, behavior: smooth ? "smooth" : "auto" });
  };

  const overflowing = edges.left || edges.right;

  return (
    <div className={cn("flex min-w-0 items-center gap-1", className)}>
      {overflowing ? (
        <Button variant="ghost" size="icon-sm" aria-label={t("scrollLeft")} disabled={!edges.left} onClick={() => scrollBy(-1)}>
          <ChevronLeft aria-hidden />
        </Button>
      ) : null}
      <div ref={scrollerRef} role="group" aria-label={label} className="scrollbar-none flex min-w-0 flex-1 gap-1.5 overflow-x-auto py-0.5 [mask-image:linear-gradient(to_right,var(--fade-left,black)_0,black_40px,black_calc(100%_-_40px),var(--fade-right,black)_100%)]">
        {items.map((item) => {
          const selected = item.value === value;
          return (
            <button
              key={item.value}
              type="button"
              aria-pressed={selected}
              onClick={() => onValueChange(item.value)}
              onFocus={(event) => event.currentTarget.scrollIntoView({ block: "nearest", inline: "nearest" })}
              className={cn(
                "flex h-7 shrink-0 outline-none focus-visible:ring-2 focus-visible:ring-signal-fg cursor-pointer items-center gap-1.5 rounded-full border px-3 text-xs font-medium whitespace-nowrap transition-colors duration-150 ease-switch",
                selected ? "border-signal bg-signal-wash text-signal-fg" : "border-line-strong text-fg-muted hover:border-fg-subtle hover:text-fg",
              )}
            >
              {item.label}
              {item.count != null ? <span className={cn("font-mono text-[11px]", selected ? "text-signal-fg" : "text-fg-muted")}>{item.count}</span> : null}
            </button>
          );
        })}
      </div>
      {overflowing ? (
        <Button variant="ghost" size="icon-sm" aria-label={t("scrollRight")} disabled={!edges.right} onClick={() => scrollBy(1)}>
          <ChevronRight aria-hidden />
        </Button>
      ) : null}
    </div>
  );
}
