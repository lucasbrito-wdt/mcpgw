// Ported from design/v5/src/components/admin-logs/logs-states.tsx on 2026-10-31.
// The "runs" empty state is dropped: executions are the list itself here.
"use client";

import { CloudOff, SearchX, Workflow } from "lucide-react";

import { useT } from "../../lib/i18n";
import { adminLogs } from "../../lib/i18n/messages/adminLogs";
import { Button } from "../ui/button";
import { EmptyState } from "../ui/empty-state";

export function NoMatchingExecutions({ query, onClear }: { query: string; onClear: () => void }) {
  const t = useT(adminLogs);
  return (
    <EmptyState
      icon={SearchX}
      title={query.trim() ? t("noMatchQuery", { query: query.trim() }) : t("noMatchFilter")}
      action={
        <Button variant="secondary" onClick={onClear}>
          {t("clearAll")}
        </Button>
      }
    >
      {t("noMatchBody")}
    </EmptyState>
  );
}

export function NoExecutions() {
  const t = useT(adminLogs);
  return (
    <EmptyState icon={Workflow} title={t("emptyTitle")}>
      {t("emptyBody")}
    </EmptyState>
  );
}

/** Reading the log failed; executions are still being recorded by the gateway. */
export function LogsLoadError({ message, onRetry }: { message: string; onRetry: () => void }) {
  const t = useT(adminLogs);
  return (
    <div role="alert" className="flex flex-col items-start gap-3 rounded-card border border-cut/35 bg-cut-wash p-6 sm:p-8">
      <span className="grid size-9 place-items-center rounded-control bg-cut/15 text-cut" aria-hidden>
        <CloudOff className="size-4" />
      </span>
      <div className="max-w-prose">
        <p className="text-sm font-semibold text-fg">{t("errorTitle")}</p>
        <p className="mt-1 text-sm text-fg-muted">
          {t("errorBody", { message })}
        </p>
      </div>
      <Button variant="secondary" onClick={onRetry}>
        {t("retry")}
      </Button>
    </div>
  );
}
