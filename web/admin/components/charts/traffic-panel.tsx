"use client";

import { useT } from "../../lib/i18n";
import { charts } from "../../lib/i18n/messages/charts";
import type { Stats } from "../../lib/stats-types";
import { LoadError } from "../admin-servers/load-states";
import { Skeleton } from "../ui/skeleton";

import { TrafficChart } from "./TrafficChart";

export interface TrafficPanelProps {
  stats: Stats | undefined;
  error: Error | undefined;
  onRetry: () => void;
}

/** Chart card with its loading, empty and error states. */
export function TrafficPanel({ stats, error, onRetry }: TrafficPanelProps) {
  const t = useT(charts);
  if (error && !stats) return <LoadError error={error} onRetry={onRetry} />;
  if (!stats) return <Skeleton role="status" aria-label={t("loadingTraffic")} aria-hidden={false} className="h-56 w-full rounded-card" />;
  return (
    <div className="rounded-card border border-line bg-panel/70 p-4">
      {stats.totals.calls === 0 ? (
        <p className="grid h-40 place-items-center text-sm text-fg-muted">{t("emptyPeriod")}</p>
      ) : (
        <TrafficChart stats={stats} />
      )}
    </div>
  );
}
