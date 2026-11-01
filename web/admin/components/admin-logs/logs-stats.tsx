"use client";

import { useFormat, useT } from "../../lib/i18n";
import { adminLogs } from "../../lib/i18n/messages/adminLogs";
import type { Stats, StatsWindow } from "../../lib/stats-types";
import { TrafficPanel } from "../charts/traffic-panel";
import { Skeleton } from "../ui/skeleton";

import { callsPerMinute, formatLatency, formatPerMinute } from "./logs-format";

function Kpi({ term, value, tone }: { term: string; value: string; tone?: "cut" }) {
  return (
    <div className="rounded-card border border-line bg-panel/70 px-4 py-3">
      <dt className="text-xs text-fg-muted">{term}</dt>
      <dd className={tone === "cut" ? "mt-1 font-mono text-2xl font-medium text-cut tabular-nums" : "mt-1 font-mono text-2xl font-medium text-fg tabular-nums"}>{value}</dd>
    </div>
  );
}

export interface LogsStatsProps {
  period: StatsWindow;
  stats: Stats | undefined;
  error: Error | undefined;
  onRetry: () => void;
}

/** KPIs (calls, calls per minute, denied, p95) and the traffic chart for the selected period. */
export function LogsStats({ period, stats, error, onRetry }: LogsStatsProps) {
  const t = useT(adminLogs);
  const f = useFormat();
  const totals = stats?.totals;
  return (
    <div className="flex flex-col gap-3">
      {totals ? (
        <dl className="grid grid-cols-2 gap-3 xl:grid-cols-4">
          <Kpi term={t("kpiCalls")} value={f.number(totals.calls)} />
          <Kpi term={t("kpiPerMinute")} value={formatPerMinute(callsPerMinute(totals.calls, period))} />
          <Kpi term={t("kpiDenied")} value={f.number(totals.denied)} tone={totals.denied > 0 ? "cut" : undefined} />
          <Kpi term={t("kpiP95")} value={totals.p95_ms === null ? t("noData") : formatLatency(totals.p95_ms)} />
        </dl>
      ) : error ? null : (
        <div className="grid grid-cols-2 gap-3 xl:grid-cols-4" aria-hidden>
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-[70px] rounded-card" />
          ))}
        </div>
      )}
      <TrafficPanel stats={stats} error={error} onRetry={onRetry} />
    </div>
  );
}
