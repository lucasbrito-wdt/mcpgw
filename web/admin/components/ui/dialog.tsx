// Ported from design/v5/src/components/ui/dialog.tsx on 2026-10-31.
"use client";

import { Dialog as BaseDialog } from "@base-ui/react/dialog";
import { X } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import type { ReactNode } from "react";

import { useT } from "../../lib/i18n";
import { ui } from "../../lib/i18n/messages/ui";
import { DURATION, EASE_SWITCH } from "../../lib/motion";
import { cn } from "../../lib/utils";

import { Button } from "./button";

interface OverlayProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  /** Action row at the bottom (primary action last). */
  footer?: ReactNode;
  className?: string;
}

export interface DialogProps extends OverlayProps {
  size?: "sm" | "md" | "lg" | "xl";
}

export interface DrawerProps extends OverlayProps {
  side?: "right" | "left";
  /** Hide the header (title stays for screen readers). Used by the mobile nav drawer. */
  bareHeader?: boolean;
}

const BACKDROP = "fixed inset-0 z-50 bg-void/70 backdrop-blur-[2px]";

function Backdrop() {
  return (
    <BaseDialog.Backdrop
      className={BACKDROP}
      render={<motion.div initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} transition={{ duration: DURATION.state }} />}
    />
  );
}

function CloseButton() {
  const t = useT(ui);
  return (
    <BaseDialog.Close render={<Button variant="ghost" size="icon-sm" aria-label={t("close")} />}>
      <X aria-hidden />
    </BaseDialog.Close>
  );
}

function Header({ title, description, srOnly = false }: { title: ReactNode; description?: ReactNode; srOnly?: boolean }) {
  return (
    <div className={cn("flex items-start gap-4", srOnly && "sr-only")}>
      <div className="min-w-0 flex-1">
        <BaseDialog.Title className="text-base font-semibold text-fg">{title}</BaseDialog.Title>
        {description ? <BaseDialog.Description className="mt-1 text-sm text-fg-muted">{description}</BaseDialog.Description> : null}
      </div>
      {srOnly ? null : <CloseButton />}
    </div>
  );
}

const DIALOG_WIDTH = { sm: "max-w-sm", md: "max-w-lg", lg: "max-w-2xl", xl: "max-w-4xl" } as const;

/** Modal dialog: focus trapped, Esc and backdrop close, focus returns to the opener. Controlled by open/onOpenChange. */
export function Dialog({ open, onOpenChange, title, description, children, footer, size = "md", className }: DialogProps) {
  const reduceMotion = useReducedMotion();
  const offset = reduceMotion ? 0 : 12;
  return (
    <BaseDialog.Root open={open} onOpenChange={(next) => onOpenChange(next)}>
      <AnimatePresence>
        {open ? (
          <BaseDialog.Portal keepMounted>
            <Backdrop />
            <BaseDialog.Popup
              className={cn(
                "fixed top-1/2 left-1/2 z-50 flex max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2 flex-col gap-5 overflow-y-auto rounded-card border border-line-strong bg-panel p-6 shadow-[0_24px_64px_-16px_rgb(0_0_0/0.7)] outline-none",
                DIALOG_WIDTH[size],
                className,
              )}
              render={
                <motion.div
                  initial={{ opacity: 0, y: offset, scale: reduceMotion ? 1 : 0.98 }}
                  animate={{ opacity: 1, y: 0, scale: 1 }}
                  exit={{ opacity: 0, y: offset / 2, scale: reduceMotion ? 1 : 0.98 }}
                  transition={{ duration: DURATION.enter, ease: EASE_SWITCH }}
                />
              }
            >
              <Header title={title} description={description} />
              {children}
              {footer ? <div className="flex flex-wrap items-center justify-end gap-2 pt-1">{footer}</div> : null}
            </BaseDialog.Popup>
          </BaseDialog.Portal>
        ) : null}
      </AnimatePresence>
    </BaseDialog.Root>
  );
}

/** Side sheet with the same contract as Dialog. Slides from the right (details) or left (mobile nav). */
export function Drawer({ open, onOpenChange, title, description, children, footer, side = "right", bareHeader = false, className }: DrawerProps) {
  const reduceMotion = useReducedMotion();
  const hidden = reduceMotion ? 0 : side === "right" ? "100%" : "-100%";
  return (
    <BaseDialog.Root open={open} onOpenChange={(next) => onOpenChange(next)}>
      <AnimatePresence>
        {open ? (
          <BaseDialog.Portal keepMounted>
            <Backdrop />
            <BaseDialog.Popup
              className={cn(
                "fixed inset-y-0 z-50 flex w-[min(28rem,calc(100vw-3rem))] flex-col gap-5 overflow-y-auto border-line-strong bg-panel p-6 outline-none",
                side === "right" ? "right-0 border-l" : "left-0 border-r",
                className,
              )}
              render={
                <motion.div
                  initial={{ x: hidden, opacity: reduceMotion ? 0 : 1 }}
                  animate={{ x: 0, opacity: 1 }}
                  exit={{ x: hidden, opacity: reduceMotion ? 0 : 1 }}
                  transition={{ duration: DURATION.enter, ease: EASE_SWITCH }}
                />
              }
            >
              <Header title={title} description={description} srOnly={bareHeader} />
              {children}
              {footer ? <div className="mt-auto flex flex-wrap items-center justify-end gap-2 pt-1">{footer}</div> : null}
            </BaseDialog.Popup>
          </BaseDialog.Portal>
        ) : null}
      </AnimatePresence>
    </BaseDialog.Root>
  );
}

export { CloseButton as DialogCloseButton };
