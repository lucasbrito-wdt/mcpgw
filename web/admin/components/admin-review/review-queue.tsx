// Ported from design/v5/src/components/admin-review/review-queue.tsx on 2026-10-31.
// The per-item summary, the injection flag and the detection time are omitted (no contract data, plan gap G4).
"use client";

import type { Tool } from "../../lib/admin-types";
import { useT } from "../../lib/i18n";
import { adminReview } from "../../lib/i18n/messages/adminReview";
import { cn } from "../../lib/utils";
import { ServiceLogo } from "../brand/ServiceLogo";
import { Button } from "../ui/button";

import { toolKey } from "./tool-key";

export interface ReviewQueueProps {
  items: readonly Tool[];
  selected: string | null;
  onSelect: (key: string) => void;
  onApproveAll: (server: string) => void;
}

/** Pending tools grouped by server. Tool names are untrusted text. */
export function ReviewQueue({ items, selected, onSelect, onApproveAll }: ReviewQueueProps) {
  const t = useT(adminReview);
  const servers = Array.from(new Set(items.map((item) => item.server)));
  return (
    <nav aria-label={t("queueAria")} className="flex flex-col gap-4 rounded-card border border-line bg-panel/70 p-2">
      {servers.map((slug) => {
        const inServer = items.filter((item) => item.server === slug);
        return (
          <div key={slug} className="flex flex-col gap-1">
            <div className="flex items-center justify-between gap-2 px-2 pt-1.5">
              <p className="flex min-w-0 items-center gap-2 text-xs font-medium text-fg-muted">
                <ServiceLogo slug={slug} name={slug} size="xs" />
                <span className="truncate font-mono">{slug}</span>
                <span className="font-mono text-[11px]">{inServer.length}</span>
              </p>
              <Button variant="ghost" size="sm" onClick={() => onApproveAll(slug)} aria-label={t("approveAllAria", { slug })}>
                {t("approveAll")}
              </Button>
            </div>
            <ul className="flex flex-col gap-0.5">
              {inServer.map((item) => {
                const key = toolKey(item);
                const isSelected = key === selected;
                return (
                  <li key={key}>
                    <button
                      type="button"
                      aria-current={isSelected ? "true" : undefined}
                      onClick={() => onSelect(key)}
                      className={cn(
                        "flex w-full cursor-pointer flex-col gap-0.5 rounded-control border px-2.5 py-2 text-left outline-none transition-colors duration-150 ease-switch focus-visible:ring-2 focus-visible:ring-signal-fg active:bg-white/8",
                        isSelected ? "border-signal bg-signal-wash" : "border-transparent hover:bg-white/5",
                      )}
                    >
                      <span className={cn("font-mono text-[13px] break-all", isSelected ? "text-signal-fg" : "text-fg")}>{item.name}</span>
                      <span className="text-xs text-fg-muted">{item.pending_changes?.length ? t("changed") : t("newTool")}</span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </div>
        );
      })}
    </nav>
  );
}
