// Ported from design/v5/src/components/admin-logs/logs-format.ts on 2026-10-31 (mock-clock helpers dropped).
import { formatNumber, getLocale, translate } from "../../lib/i18n";
import { adminLogs } from "../../lib/i18n/messages/adminLogs";
import { STATS_WINDOWS, type StatsWindow } from "../../lib/stats-types";
import type { BadgeTone } from "../ui/badge";

import type { Execution } from "./types";

export type StatusFilter = "all" | "running" | "succeeded" | "failed";

// Label helpers read the active locale at call time; the components that call them also call useT, so a switch re-renders them.
const tr = (key: keyof typeof adminLogs.en) => translate(getLocale(), adminLogs, key);

export const STATUS_FILTERS: readonly StatusFilter[] = ["all", "running", "succeeded", "failed"];

export const statusFilterLabel = (filter: StatusFilter): string => tr(`filter_${filter}`);

const STATUS_TONE = { running: "signal", succeeded: "ok", failed: "cut", timed_out: "warn", cancelled: "neutral" } as const;
type KnownStatus = keyof typeof STATUS_TONE;
const isKnownStatus = (status: string): status is KnownStatus => Object.hasOwn(STATUS_TONE, status);

export const statusMeta = (status: string): { label: string; tone: BadgeTone } =>
  isKnownStatus(status) ? { label: tr(`status_${status}`), tone: STATUS_TONE[status] } : { label: status, tone: "neutral" };

export const kindLabel = (kind: string): string => (kind === "execute" ? tr("kind_execute") : kind === "search" ? tr("kind_search") : kind);

export function formatLatency(ms: number): string {
  return ms >= 1000 ? `${formatNumber(ms / 1000, { maximumFractionDigits: 1, minimumFractionDigits: 1 })} s` : `${ms} ms`;
}

/** Wall time of an execution; null while it has not finished. */
export function durationMs(execution: Pick<Execution, "started_at" | "finished_at">): number | null {
  if (!execution.finished_at) return null;
  const ms = Date.parse(execution.finished_at) - Date.parse(execution.started_at);
  return Number.isNaN(ms) ? null : Math.max(0, ms);
}

export function matchesQuery(execution: Execution, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return `${execution.email} ${execution.client_id} ${execution.code}`.toLowerCase().includes(q);
}

/** Newest first, by id; `incoming` replaces rows with the same id. */
export function mergeExecutions(base: readonly Execution[], incoming: readonly Execution[]): Execution[] {
  const byId = new Map<string, Execution>();
  for (const row of [...base, ...incoming]) byId.set(row.id, row);
  return Array.from(byId.values()).sort((a, b) => Date.parse(b.started_at) - Date.parse(a.started_at));
}

export type LogsWindow = Exclude<StatsWindow, "7d">;

/** Period tabs of the logs screen; 7d stays on the server detail. */
export const LOGS_WINDOWS: readonly LogsWindow[] = STATS_WINDOWS.filter((w): w is LogsWindow => w !== "7d");

export const windowLabel = (window: StatsWindow): string => tr(`window_${window}`);

const WINDOW_MINUTES: Record<StatsWindow, number> = { "5m": 5, "15m": 15, "1h": 60, "24h": 1440, "7d": 10080 };

/** Average calls per minute over the window. */
export const callsPerMinute = (calls: number, window: StatsWindow): number => calls / WINDOW_MINUTES[window];

export const formatPerMinute = (value: number): string => formatNumber(value, { maximumFractionDigits: 1 });
