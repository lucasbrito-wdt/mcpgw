// Ported from design/v5/src/components/admin-logs/live-controls.tsx on 2026-10-31.
// The per-minute and p95 stats are omitted: the contract has no per-call stream (plan gap G4).
"use client";

import { useT } from "../../lib/i18n";
import { adminLive } from "../../lib/i18n/messages/adminLive";
import { cn } from "../../lib/utils";
import { Toggle } from "../ui/toggle";

export const POLL_MS = 5000;
export const BACKOFF_MS = 30000;
export const BACKOFF_AFTER_ERRORS = 3;

/** Milliseconds until the next poll; null while the tab is hidden (the caller resumes on visibilitychange). */
export function nextPollDelay({ errors, visible }: { errors: number; visible: boolean }): number | null {
  if (!visible) return null;
  return errors >= BACKOFF_AFTER_ERRORS ? BACKOFF_MS : POLL_MS;
}

export interface LiveToggleProps {
  live: boolean;
  onLiveChange: (live: boolean) => void;
  disabled?: boolean;
}

/** "Ao vivo" switch: while on, the list polls the log and new rows enter at the top. */
export function LiveToggle({ live, onLiveChange, disabled }: LiveToggleProps) {
  const t = useT(adminLive);
  return (
    <div className="flex h-10 items-center gap-2.5 rounded-card border border-line-strong bg-ink/60 px-3">
      <Toggle checked={live} onCheckedChange={onLiveChange} label={t("label")} disabled={disabled} />
      <span aria-hidden className={cn("text-[13px] font-semibold", disabled ? "text-fg-muted" : "text-fg")}>
        {t("label")}
      </span>
      <span aria-hidden className="relative grid size-2 place-items-center">
        {live ? <span className="absolute inset-0 animate-ping rounded-full bg-ok/60 motion-reduce:hidden" /> : null}
        <span className={cn("size-2 rounded-full", live ? "bg-ok" : "bg-fg-subtle")} />
      </span>
      <p aria-live="polite" className="sr-only">
        {disabled ? "" : live ? t("on") : t("off")}
      </p>
    </div>
  );
}
