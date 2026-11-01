"use client";

import { ScrollText } from "lucide-react";

import { useLoad, type AdminEvent, type Paged } from "../../lib/admin-types";
import { formatDateTime } from "../../lib/format";
import { hasKey, useT } from "../../lib/i18n";
import { servers } from "../../lib/i18n/messages/servers";
import { EmptyState } from "../ui/empty-state";

import { LoadError, RowsSkeleton } from "./load-states";

const WINDOW = 200;

const concerns = (event: AdminEvent, slug: string) => event.target === slug || event.target.startsWith(`${slug}/`);

/** Admin log entries whose target is this server or one of its tools (last 200 events of the instance). */
export function DetailActivity({ slug }: { slug: string }) {
  const t = useT(servers);
  const events = useLoad<Paged<AdminEvent>>(`/admin-events?limit=${WINDOW}`);
  if (events.error) return <LoadError error={events.error} onRetry={events.reload} />;
  if (!events.data) return <RowsSkeleton rows={4} label={t("activityLoading")} />;

  const actionKey = (action: string) => `action_${action.replace(".", "_")}`;
  const actionLabel = (action: string) => {
    const key = actionKey(action);
    return hasKey(servers, key) ? t(key) : action;
  };

  const mine = events.data.items.filter((event) => concerns(event, slug));
  if (mine.length === 0) {
    return (
      <EmptyState icon={ScrollText} title={t("activityEmptyTitle")}>
        {t("activityEmptyBody")}
      </EmptyState>
    );
  }
  return (
    <ol className="flex flex-col divide-y divide-line rounded-card border border-line bg-panel/70" aria-label={t("activityLabel", { slug })}>
      {mine.map((event) => (
        <li key={event.id} className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 px-4 py-3 text-[13px]">
          <span className="min-w-0">
            <span className="font-medium text-fg">{actionLabel(event.action)}</span>
            {event.target !== slug ? <code className="ml-2 font-mono text-xs break-all text-fg-muted">{event.target}</code> : null}
            <span className="block text-xs text-fg-muted">{event.actor_email}</span>
          </span>
          <time dateTime={event.ts} className="shrink-0 font-mono text-xs text-fg-muted">
            {formatDateTime(event.ts)}
          </time>
        </li>
      ))}
    </ol>
  );
}
