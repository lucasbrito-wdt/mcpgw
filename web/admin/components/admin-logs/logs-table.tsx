// Ported from design/v5/src/components/admin-logs/logs-table.tsx on 2026-10-31.
// Minute groups, route glyph and latency bar are dropped: the list contract has executions, not per-call routes.
"use client";

import { formatDateTime, truncate } from "../../lib/format";
import { useT } from "../../lib/i18n";
import { adminLogs } from "../../lib/i18n/messages/adminLogs";
import { cn } from "../../lib/utils";
import { Badge } from "../ui/badge";

import { durationMs, formatLatency, kindLabel, statusMeta } from "./logs-format";
import type { Execution } from "./types";

export interface LogsTableProps {
  executions: readonly Execution[];
  /** Newest row id while live; gets a short wash so the eye finds it. */
  freshId?: string;
  selectedId?: string | null;
  onOpen: (execution: Execution) => void;
}

const SNIPPET_MAX = 120;
const SLOW_MS = 2000;

/** Wide: Quando | Código | Pessoa | Cliente | Duração | Resultado. Narrow: each row is a card. */
const GRID = "@min-[60rem]:grid-cols-[8.5rem_minmax(12rem,2fr)_minmax(8rem,1fr)_minmax(7rem,1fr)_4.5rem_minmax(8.5rem,auto)]";

function firstLine(code: string): string {
  return truncate(code.split("\n").find((line) => line.trim() !== "")?.trim() ?? "", SNIPPET_MAX);
}

export function LogsTable({ executions, freshId, selectedId, onOpen }: LogsTableProps) {
  const t = useT(adminLogs);
  return (
    <div className="@container rounded-card border border-line bg-panel/70">
      <div aria-hidden className={cn("hidden px-4 pt-3 pb-1.5 text-xs font-medium text-fg-muted @min-[60rem]:grid @min-[60rem]:gap-x-4", GRID)}>
        <span>{t("colWhen")}</span>
        <span>{t("colCode")}</span>
        <span>{t("colPerson")}</span>
        <span>{t("colClient")}</span>
        <span className="text-right">{t("colDuration")}</span>
        <span>{t("colResult")}</span>
      </div>
      <ul aria-label={t("listAria")} className="flex flex-col p-1.5">
        {executions.map((execution) => (
          <li key={execution.id}>
            <ExecutionRow execution={execution} fresh={execution.id === freshId} selected={execution.id === selectedId} onOpen={onOpen} />
          </li>
        ))}
      </ul>
    </div>
  );
}

interface ExecutionRowProps {
  execution: Execution;
  fresh: boolean;
  selected: boolean;
  onOpen: (execution: Execution) => void;
}

function ExecutionRow({ execution, fresh, selected, onOpen }: ExecutionRowProps) {
  const t = useT(adminLogs);
  const meta = statusMeta(execution.status);
  const duration = durationMs(execution);
  const snippet = firstLine(execution.code);
  return (
    <button
      type="button"
      aria-haspopup="dialog"
      aria-expanded={selected}
      onClick={() => onOpen(execution)}
      className={cn(
        "grid w-full cursor-pointer grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-1.5 rounded-control px-3 py-2.5 text-left outline-none transition-colors duration-150 ease-switch motion-reduce:transition-none",
        "hover:bg-white/4 active:bg-white/7 focus-visible:ring-2 focus-visible:ring-signal-fg aria-expanded:bg-signal-wash/70",
        "@min-[60rem]:gap-x-4 @min-[60rem]:gap-y-0 @min-[60rem]:py-2",
        GRID,
        fresh && "bg-signal-wash/50 ring-1 ring-signal/40 ring-inset",
      )}
    >
      <span className="font-mono text-xs whitespace-nowrap text-fg-muted tabular-nums">{formatDateTime(execution.started_at)}</span>
      <span className="col-span-2 min-w-0 @min-[60rem]:col-span-1">
        <span className="block truncate font-mono text-[13px] text-fg">{snippet || t("noCode")}</span>
        {execution.code_truncated ? <span className="block text-[11px] text-fg-muted">{t("codeTruncated")}</span> : null}
      </span>
      <span className="truncate text-[13px] text-fg-muted @min-[60rem]:text-fg">{execution.email}</span>
      <span className="hidden truncate text-[13px] text-fg-muted @min-[60rem]:block">{execution.client_id}</span>
      <span className={cn("font-mono text-xs whitespace-nowrap tabular-nums @min-[60rem]:text-right", duration !== null && duration >= SLOW_MS ? "text-warn" : "text-fg-muted")}>
        {duration === null ? "-" : formatLatency(duration)}
        <span className="ml-2 @min-[60rem]:hidden">
          {execution.inner_calls === 1 ? t("callsOne") : t("callsMany", { n: execution.inner_calls })}
        </span>
      </span>
      <span className="col-start-2 row-start-1 flex items-center gap-1.5 justify-self-end @min-[60rem]:col-start-auto @min-[60rem]:row-start-auto @min-[60rem]:justify-self-start">
        <Badge tone={meta.tone}>{meta.label}</Badge>
        <span className="hidden text-xs text-fg-muted @min-[70rem]:inline">{kindLabel(execution.kind)}</span>
      </span>
    </button>
  );
}
