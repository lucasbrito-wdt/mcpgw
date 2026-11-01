// Ported from design/v5/src/components/admin-logs/call-drawer.tsx on 2026-10-31.
// Shows what the contract has: the execution, its full code and the audit events of its inner calls.
"use client";

import { Ban, CircleAlert, CircleCheck } from "lucide-react";
import { useState, type ReactNode } from "react";

import { useLoad } from "../../lib/admin-types";
import { formatDateTime } from "../../lib/format";
import { useT } from "../../lib/i18n";
import { adminLogs } from "../../lib/i18n/messages/adminLogs";
import { formatLatency, durationMs, kindLabel, statusMeta } from "../admin-logs/logs-format";
import type { ExecutionAuditEvent, ExecutionDetail } from "../admin-logs/types";
import { LoadError, RowsSkeleton } from "../admin-servers/load-states";
import { Badge } from "../ui/badge";
import { Drawer } from "../ui/dialog";

export interface ExecutionDrawerProps {
  /** Execution to show; null keeps the drawer closed. */
  executionId: string | null;
  onClose: () => void;
}

/** Detail of one execution. Code, errors and tool names are untrusted and only ever rendered as text. */
export function ExecutionDrawer({ executionId, onClose }: ExecutionDrawerProps) {
  const t = useT(adminLogs);
  // Keep the last id while the drawer animates out.
  const [last, setLast] = useState(executionId);
  if (executionId && executionId !== last) setLast(executionId);
  const shownId = executionId ?? last;
  const detail = useLoad<ExecutionDetail>(shownId ? `/executions/${encodeURIComponent(shownId)}` : null);

  return (
    <Drawer
      open={executionId !== null}
      onOpenChange={(open) => (open ? null : onClose())}
      title={t("execution")}
      description={detail.data ? t("drawerDescription", { email: detail.data.execution.email, time: formatDateTime(detail.data.execution.started_at) }) : undefined}
      className="w-[min(34rem,calc(100vw-1.5rem))] gap-6"
    >
      {detail.error ? <LoadError error={detail.error} onRetry={detail.reload} /> : null}
      {!detail.error && !detail.data ? <RowsSkeleton rows={3} label={t("loadingExecution")} /> : null}
      {detail.data ? <ExecutionBody detail={detail.data} /> : null}
    </Drawer>
  );
}

function ExecutionBody({ detail }: { detail: ExecutionDetail }) {
  const t = useT(adminLogs);
  const { execution, audit_events: events } = detail;
  const meta = statusMeta(execution.status);
  const duration = durationMs(execution);
  return (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone={meta.tone}>{meta.label}</Badge>
        <Badge>{kindLabel(execution.kind)}</Badge>
      </div>
      {execution.error ? (
        <p role="alert" className="flex items-start gap-2 rounded-card bg-cut-wash p-3 text-sm text-fg ring-1 ring-cut/35 ring-inset">
          <CircleAlert className="mt-0.5 size-4 shrink-0 text-cut" aria-hidden />
          <span className="min-w-0 break-words">{execution.error}</span>
        </p>
      ) : null}
      <Section title={t("sectionCode")}>
        <pre className="max-h-80 overflow-auto rounded-control border border-line bg-ink/70 p-3 font-mono text-xs leading-5 whitespace-pre-wrap break-words text-fg-muted">{execution.code}</pre>
      </Section>
      <Section title={t("execution")}>
        <dl className="grid grid-cols-[minmax(7rem,auto)_minmax(0,1fr)] gap-x-4 gap-y-2 text-[13px]">
          <Fact term={t("factPerson")} value={execution.email} />
          <Fact term={t("factClient")} value={execution.client_id} mono />
          <Fact term={t("factStart")} value={formatDateTime(execution.started_at)} />
          <Fact term={t("factEnd")} value={execution.finished_at ? formatDateTime(execution.finished_at) : t("stillRunning")} />
          <Fact term={t("factDuration")} value={duration === null ? t("factNoData") : formatLatency(duration)} />
          <Fact term={t("factInnerCalls")} value={String(execution.inner_calls)} />
          <Fact term={t("factId")} value={execution.id} mono />
        </dl>
      </Section>
      <Section title={t("sectionToolCalls")} note={t("toolCallsNote")}>
        {events.length === 0 ? <p className="text-sm text-fg-muted">{t("noToolCalls")}</p> : <AuditEvents events={events} />}
      </Section>
    </>
  );
}

function AuditEvents({ events }: { events: readonly ExecutionAuditEvent[] }) {
  const t = useT(adminLogs);
  return (
    <ul className="flex flex-col divide-y divide-line rounded-card border border-line bg-panel/70">
      {events.map((event, index) => {
        const denied = event.decision === "deny";
        const Icon = denied ? Ban : CircleCheck;
        return (
          <li key={`${event.ts}-${index}`} className="flex items-start gap-3 px-3 py-2.5">
            <Icon className={denied ? "mt-0.5 size-4 shrink-0 text-cut" : "mt-0.5 size-4 shrink-0 text-ok"} aria-hidden />
            <div className="min-w-0 flex-1">
              <p className="truncate font-mono text-[13px] text-fg">
                {event.server}.{event.tool}
              </p>
              <p className="text-xs text-fg-muted">
                {denied ? t("denied") : t("allowed")}
                {event.reason ? `: ${event.reason}` : ""}
              </p>
            </div>
            <span className="shrink-0 text-right font-mono text-xs text-fg-muted tabular-nums">
              {event.latency_ms == null ? "" : formatLatency(event.latency_ms)}
              <span className="block">{formatDateTime(event.ts).slice(-5)}</span>
            </span>
          </li>
        );
      })}
    </ul>
  );
}

function Section({ title, note, children }: { title: string; note?: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2.5">
      <div>
        <h3 className="text-[13px] font-semibold text-fg">{title}</h3>
        {note ? <p className="text-xs text-fg-muted">{note}</p> : null}
      </div>
      {children}
    </section>
  );
}

function Fact({ term, value, mono = false }: { term: string; value: string; mono?: boolean }) {
  return (
    <div className="col-span-full grid grid-cols-subgrid items-start">
      <dt className="text-fg-muted">{term}</dt>
      <dd className={mono ? "min-w-0 font-mono text-[13px] break-all text-fg" : "min-w-0 text-fg"}>{value}</dd>
    </div>
  );
}
