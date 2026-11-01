"use client";

import { CircleAlert, RotateCw } from "lucide-react";

import { useT } from "../../lib/i18n";
import { servers } from "../../lib/i18n/messages/servers";
import { Button } from "../ui/button";
import { EmptyState } from "../ui/empty-state";
import { Skeleton } from "../ui/skeleton";

/** The request failed: say so in product words and offer the retry. */
export function LoadError({ error, onRetry }: { error: Error; onRetry: () => void }) {
  const t = useT(servers);
  return (
    <EmptyState
      icon={CircleAlert}
      title={t("loadFailed")}
      action={
        <Button variant="secondary" onClick={onRetry}>
          <RotateCw aria-hidden />
          {t("retry")}
        </Button>
      }
    >
      {error.message}
    </EmptyState>
  );
}

/** Placeholder rows while a list loads; the container announces the wait to screen readers. */
export function RowsSkeleton({ rows = 5, label }: { rows?: number; label?: string }) {
  const t = useT(servers);
  return (
    <div role="status" aria-label={label ?? t("loading")} className="flex flex-col gap-2">
      {Array.from({ length: rows }, (_, index) => (
        <Skeleton key={index} className="h-14 w-full rounded-card" />
      ))}
    </div>
  );
}
