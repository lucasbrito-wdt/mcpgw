// Ported from design/v5/src/components/ui/copy-button.tsx on 2026-10-31.
"use client";

import { Check, Copy, TriangleAlert } from "lucide-react";
import { AnimatePresence } from "motion/react";
import { useEffect, useRef, useState } from "react";

import { Button } from "./button";
import { MotionSpan } from "./motion-span";
import { useT } from "../../lib/i18n";
import { ui } from "../../lib/i18n/messages/ui";
import { DURATION, EASE_SWITCH } from "../../lib/motion";
import { cn } from "../../lib/utils";

type CopyState = "idle" | "copied" | "failed";

const RESET_MS = 2000;

const LOOK: Record<CopyState, { icon: typeof Copy; text: "copy" | "copied" | "copyManually" }> = {
  idle: { icon: Copy, text: "copy" },
  copied: { icon: Check, text: "copied" },
  failed: { icon: TriangleAlert, text: "copyManually" },
};

export interface CopyButtonProps {
  value: string;
  /** What is copied, for the accessible name ("Copy MCP address"). */
  label: string;
  /** Visible text when idle; defaults to the translated "Copy". */
  text?: string;
  variant?: "secondary" | "primary" | "ghost";
  size?: "sm" | "md";
  className?: string;
}

/** Copies to the clipboard and says "Copied" for 2 s. If the browser blocks it, asks for a manual copy. */
export function CopyButton({ value, label, text, variant = "secondary", size = "sm", className }: CopyButtonProps) {
  const t = useT(ui);
  const [state, setState] = useState<CopyState>("idle");
  const timer = useRef(0);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setState("copied");
    } catch {
      setState("failed");
    }
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setState("idle"), RESET_MS);
  }

  const look = LOOK[state];
  const Icon = look.icon;
  const lookText = t(look.text);
  return (
    <Button variant={variant} size={size} onClick={copy} aria-label={state === "idle" ? label : lookText} className={className}>
      <span className="grid">
      <AnimatePresence initial={false}>
        <MotionSpan
          key={state}
          initial={{ opacity: 0, y: 4 }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0, y: -4 }}
          transition={{ duration: DURATION.state, ease: EASE_SWITCH }}
          className={cn("col-start-1 row-start-1 inline-flex items-center justify-center gap-1.5", state === "copied" && "text-ok")}
        >
          <Icon aria-hidden className="size-3.5" />
          {state === "idle" ? (text ?? lookText) : lookText}
        </MotionSpan>
      </AnimatePresence>
      </span>
      <span aria-live="polite" className="sr-only">
        {state === "copied" ? t("copied") : ""}
      </span>
    </Button>
  );
}
