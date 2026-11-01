// Same sticky bar as design/v5/src/components/admin-connections/bulk-bar.tsx, for the portal selection.
"use client";

import { AnimatePresence, motion } from "motion/react";

import { Button } from "../ui/button";
import { countLabel } from "../../hooks/countLabel";
import { usePrefersReducedMotion } from "../../hooks/usePrefersReducedMotion";
import { useT } from "../../lib/i18n";
import { portalServers } from "../../lib/i18n/messages/portalServers";
import { SWITCH_TRANSITION } from "../../lib/motion";

export interface BulkBarProps {
  count: number;
  onClear: () => void;
  onDisconnect: () => void;
}

/** Sticky action bar for the selected personal connections; the one orchestrated movement on the grid. */
export function BulkBar({ count, onClear, onDisconnect }: BulkBarProps) {
  const t = useT(portalServers);
  const reduce = usePrefersReducedMotion();
  const offset = reduce ? 0 : 16;
  return (
    <AnimatePresence>
      {count > 0 ? (
        <motion.div
          key="bar"
          role="region"
          aria-label={t("bulkRegion")}
          initial={{ opacity: 0, y: offset }}
          animate={{ opacity: 1, y: 0 }}
          exit={{ opacity: 0, y: offset }}
          transition={SWITCH_TRANSITION}
          className="sticky bottom-4 z-20 flex flex-wrap items-center justify-between gap-x-4 gap-y-2 rounded-card border border-line-strong bg-panel/95 p-3 shadow-2xl shadow-black/50 backdrop-blur"
        >
          <p className="text-sm text-fg" aria-live="polite">
            <span className="font-semibold">{countLabel(t, "selected", count)}</span>
          </p>
          <div className="flex items-center gap-2">
            <Button variant="secondary" size="sm" onClick={onClear}>
              {t("clearSelection")}
            </Button>
            <Button variant="danger" size="sm" onClick={onDisconnect}>
              {t("disconnectSelected")}
            </Button>
          </div>
        </motion.div>
      ) : null}
    </AnimatePresence>
  );
}
