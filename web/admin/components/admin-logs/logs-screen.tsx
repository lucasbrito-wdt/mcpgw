// Ported from design/v5/src/components/admin-logs/logs-screen.tsx on 2026-10-31.
// Hash states became React state; the switchboard hero and runs tab are omitted. Period tabs, KPIs and chart come from /stats.
"use client";

import { RotateCw } from "lucide-react";
import { useMemo, useState } from "react";

import { api, ApiError } from "../../lib/api";
import { useLoad, type Paged } from "../../lib/admin-types";
import { useT } from "../../lib/i18n";
import { adminLogs } from "../../lib/i18n/messages/adminLogs";
import type { Stats } from "../../lib/stats-types";
import { LiveToggle } from "../admin/LiveToggle";
import { ExecutionDrawer } from "../admin/ExecutionDrawer";
import { RowsSkeleton } from "../admin-servers/load-states";
import { Button } from "../ui/button";
import { SearchField } from "../ui/input";
import { SegmentedTabs } from "../ui/segmented-tabs";
import { useToast } from "../ui/toast";

import { LOGS_WINDOWS, matchesQuery, mergeExecutions, statusFilterLabel, STATUS_FILTERS, windowLabel, type LogsWindow, type StatusFilter } from "./logs-format";
import { LogsStats } from "./logs-stats";
import { LogsLoadError, NoExecutions, NoMatchingExecutions } from "./logs-states";
import { LogsTable } from "./logs-table";
import type { Execution } from "./types";
import { useLivePolling } from "./use-live-polling";

const PAGE_SIZE = 50;

interface Extra {
  /** The request path these rows belong to; rows of another filter are ignored. */
  path: string;
  items: Execution[];
  /** Cursor of the next older page after "Carregar mais"; undefined until used. */
  cursor?: string;
}

const listPath = (status: StatusFilter, cursor?: string) =>
  `/executions?limit=${PAGE_SIZE}${status === "all" ? "" : `&status=${status}`}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`;

/** Adds rows to the extras of `path`, dropping extras (and cursor) that belong to another filter. */
function addRows(current: Extra, path: string, items: readonly Execution[], cursor?: string): Extra {
  const same = current.path === path;
  return { path, items: mergeExecutions(same ? current.items : [], items), cursor: cursor ?? (same ? current.cursor : undefined) };
}

/** Executions list: newest first, filtered by status, with the live poll merging new rows by id. */
export function LogsScreen() {
  const t = useT(adminLogs);
  const [status, setStatus] = useState<StatusFilter>("all");
  const [query, setQuery] = useState("");
  const [period, setPeriod] = useState<LogsWindow>("1h");
  const [liveStats, setLiveStats] = useState<Stats | null>(null);
  const [liveWanted, setLiveWanted] = useState(true);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [extra, setExtra] = useState<Extra>({ path: "", items: [] });
  const [loadingMore, setLoadingMore] = useState(false);
  const toast = useToast();

  const path = listPath(status);
  const first = useLoad<Paged<Execution>>(path);
  const statsPath = `/stats?window=${period}`;
  const statsLoad = useLoad<Stats>(statsPath);
  const failed = first.error !== undefined;
  const live = liveWanted && !failed;

  useLivePolling(live, async () => {
    // allSettled: a failing /stats must not freeze the execution list (and vice versa).
    const [page, fresh] = await Promise.allSettled([api<Paged<Execution>>("admin", path), api<Stats>("admin", statsPath)]);
    if (page.status === "fulfilled") setExtra((current) => addRows(current, path, page.value.items));
    if (fresh.status === "fulfilled") setLiveStats(fresh.value);
    if (page.status === "rejected") throw page.reason;
  });

  const stats = liveStats?.window === period ? liveStats : statsLoad.data;

  const own = extra.path === path ? extra : null;
  const executions = useMemo(() => mergeExecutions(first.data?.items ?? [], own?.items ?? []), [first.data, own]);
  const cursor = own?.cursor ?? first.data?.next_cursor ?? "";
  const shown = executions.filter((execution) => matchesQuery(execution, query));
  const filtering = query.trim() !== "" || status !== "all";

  async function loadMore() {
    setLoadingMore(true);
    try {
      const page = await api<Paged<Execution>>("admin", listPath(status, cursor));
      setExtra((current) => addRows(current, path, page.items, page.next_cursor));
    } catch (e) {
      toast(e instanceof ApiError ? e.message : t("loadMoreError"), { tone: "cut" });
    } finally {
      setLoadingMore(false);
    }
  }

  const changePeriod = (next: LogsWindow) => {
    setLiveStats(null);
    setPeriod(next);
  };

  const clearFilters = () => {
    setQuery("");
    setStatus("all");
  };

  return (
    <div className="flex flex-col gap-4">
      <section aria-label={t("trafficAria")} className="flex flex-col gap-3">
        <SegmentedTabs
          label={t("period")}
          size="sm"
          value={period}
          onValueChange={changePeriod}
          items={LOGS_WINDOWS.map((w) => ({ value: w, label: windowLabel(w) }))}
        />
        <LogsStats period={period} stats={stats} error={statsLoad.error} onRetry={statsLoad.reload} />
      </section>

      <div className="flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between">
        <SegmentedTabs
          label={t("result")}
          controls="log-panel"
          size="sm"
          value={status}
          onValueChange={setStatus}
          items={STATUS_FILTERS.map((s) => ({ value: s, label: statusFilterLabel(s) }))}
        />
        <div className="flex flex-wrap items-center gap-2">
          <SearchField value={query} onValueChange={setQuery} label={t("searchLabel")} placeholder={t("searchPlaceholder")} className="min-w-0 flex-1 sm:w-72 sm:flex-none" />
          <LiveToggle live={live} onLiveChange={setLiveWanted} disabled={failed} />
        </div>
      </div>

      <div id="log-panel">
        {failed ? <LogsLoadError message={first.error?.message ?? ""} onRetry={first.reload} /> : null}
        {!failed && !first.data ? <RowsSkeleton label={t("loading")} /> : null}
        {first.data && executions.length === 0 && !filtering ? <NoExecutions /> : null}
        {first.data && shown.length === 0 && (executions.length > 0 || filtering) ? <NoMatchingExecutions query={query} onClear={clearFilters} /> : null}
        {shown.length > 0 ? (
          <div className="flex flex-col gap-3">
            <LogsTable executions={shown} freshId={live ? executions[0]?.id : undefined} selectedId={selectedId} onOpen={(execution) => setSelectedId(execution.id)} />
            <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-fg-muted">
              <p>
                {t("loadedCount", { shown: shown.length, total: executions.length })}
              </p>
              {cursor ? (
                <Button variant="secondary" size="sm" loading={loadingMore} onClick={() => void loadMore()}>
                  <RotateCw aria-hidden />
                  {t("loadMore")}
                </Button>
              ) : null}
            </div>
          </div>
        ) : null}
      </div>

      <ExecutionDrawer executionId={selectedId} onClose={() => setSelectedId(null)} />
    </div>
  );
}
