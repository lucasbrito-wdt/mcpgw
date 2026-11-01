// Ported from design/v5/src/components/ui/popover.tsx on 2026-10-31.
"use client";

import { Popover as BasePopover } from "@base-ui/react/popover";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import type { ReactElement, ReactNode } from "react";

import { DURATION, EASE_SWITCH } from "../../lib/motion";
import { cn } from "../../lib/utils";

export interface PopoverProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The trigger element, usually a <Button />. Receives aria-expanded and aria-haspopup. */
  trigger: ReactElement;
  /** Title shown at the top of the popup and used as its accessible name. */
  title: string;
  children: ReactNode;
  align?: "start" | "center" | "end";
  className?: string;
}

/** Anchored popup for filters and small forms. Esc and outside click close it; focus returns to the trigger. */
export function Popover({ open, onOpenChange, trigger, title, children, align = "end", className }: PopoverProps) {
  const reduceMotion = useReducedMotion();
  return (
    <BasePopover.Root open={open} onOpenChange={(next) => onOpenChange(next)}>
      <BasePopover.Trigger render={trigger} />
      <AnimatePresence>
        {open ? (
          <BasePopover.Portal keepMounted>
            <BasePopover.Positioner sideOffset={6} align={align} className="z-50">
              <BasePopover.Popup
                className={cn(
                  "w-72 rounded-card border border-line-strong bg-panel p-3 shadow-[0_16px_48px_-12px_rgb(0_0_0/0.75)] outline-none",
                  className,
                )}
                render={
                  <motion.div
                    initial={{ opacity: 0, y: reduceMotion ? 0 : -4 }}
                    animate={{ opacity: 1, y: 0 }}
                    exit={{ opacity: 0, y: reduceMotion ? 0 : -4 }}
                    transition={{ duration: DURATION.state, ease: EASE_SWITCH }}
                  />
                }
              >
                <BasePopover.Title className="mb-2 px-1 text-xs font-semibold text-fg-muted">{title}</BasePopover.Title>
                {children}
              </BasePopover.Popup>
            </BasePopover.Positioner>
          </BasePopover.Portal>
        ) : null}
      </AnimatePresence>
    </BasePopover.Root>
  );
}
