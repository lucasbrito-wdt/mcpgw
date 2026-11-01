// Ported from design/v5/src/components/admin-connections/bulk-bar.tsx on 2026-10-31.
"use client";

import { AnimatePresence, motion } from "motion/react";

import { usePrefersReducedMotion } from "../../hooks/usePrefersReducedMotion";
import { useT } from "../../lib/i18n";
import { adminConnections } from "../../lib/i18n/messages/adminConnections";
import { SWITCH_TRANSITION } from "../../lib/motion";
import { Button } from "../ui/button";

import { MAX_BULK } from "./connections-model";

export interface BulkBarProps {
  count: number;
  onClear: () => void;
  onRevoke: () => void;
}

/** Sticky action bar for the selection; the one orchestrated movement on this screen. */
export function BulkBar({ count, onClear, onRevoke }: BulkBarProps) {
  const t = useT(adminConnections);
  const reduce = usePrefersReducedMotion();
  const offset = reduce ? 0 : 16;
  return (
    <AnimatePresence>
      {count > 0 ? (
        <motion.div
          key="bar"
          role="region"
          aria-label={t("bulkAria")}
          initial={{ opacity: 0, y: offset }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0, y: offset }}
          transition={SWITCH_TRANSITION}
          className="sticky bottom-4 z-20 flex flex-wrap items-center justify-between gap-x-4 gap-y-2 rounded-card border border-line-strong bg-panel/95 p-3 shadow-2xl shadow-black/50 backdrop-blur"
        >
          <p className="text-sm text-fg" aria-live="polite">
            <span className="font-semibold">{t.plural("selected", count)}</span>
            <span className="ml-2 text-xs text-fg-muted">{t("upTo", { max: MAX_BULK })}</span>
          </p>
          <div className="flex items-center gap-2">
            <Button variant="secondary" size="sm" onClick={onClear}>
              {t("clearSelection")}
            </Button>
            <Button variant="danger" size="sm" onClick={onRevoke}>
              {t("revokeSelected")}
            </Button>
          </div>
        </motion.div>
      ) : null}
    </AnimatePresence>
  );
}
