"use client";

import { RotateCw, ScrollText } from "lucide-react";
import { useState } from "react";

import { CELL, DataTable, ROW } from "../../../components/admin-logs/data-table";
import { LoadError, RowsSkeleton } from "../../../components/admin-servers/load-states";
import { AppShell } from "../../../components/shell/AppShell";
import { PageHeader } from "../../../components/shell/PageHeader";
import { Button } from "../../../components/ui/button";
import { EmptyState } from "../../../components/ui/empty-state";
import { SearchField } from "../../../components/ui/input";
import { useToast } from "../../../components/ui/toast";
import { useLoad, type AdminEvent, type Paged } from "../../../lib/admin-types";
import { api, ApiError } from "../../../lib/api";
import { formatDateTime } from "../../../lib/format";
import { useT } from "../../../lib/i18n";
import { adminLog } from "../../../lib/i18n/messages/adminLog";

const PAGE_SIZE = 50;
const listPath = (cursor?: string) => `/admin-events?limit=${PAGE_SIZE}${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`;

/** Details as `key=value` text; values are untrusted and never parsed as markup. */
function detailsText(details: Record<string, unknown>): string {
  return Object.entries(details)
    .map(([key, value]) => `${key}=${typeof value === "string" ? value : JSON.stringify(value)}`)
    .join(" ");
}

const matches = (event: AdminEvent, needle: string) => !needle || `${event.actor_email} ${event.action} ${event.target}`.toLowerCase().includes(needle);

export default function AdminLogPage() {
  const t = useT(adminLog);
  const toast = useToast();
  const first = useLoad<Paged<AdminEvent>>(listPath());
  const [older, setOlder] = useState<AdminEvent[]>([]);
  const [olderCursor, setOlderCursor] = useState<string | undefined>();
  const [loadingMore, setLoadingMore] = useState(false);
  const [query, setQuery] = useState("");

  const events = [...(first.data?.items ?? []), ...older];
  const cursor = olderCursor ?? first.data?.next_cursor ?? "";
  const needle = query.trim().toLowerCase();
  const shown = events.filter((event) => matches(event, needle));

  const reload = () => {
    setOlder([]);
    setOlderCursor(undefined);
    first.reload();
  };

  async function loadMore() {
    setLoadingMore(true);
    try {
      const page = await api<Paged<AdminEvent>>("admin", listPath(cursor));
      setOlder((current) => [...current, ...page.items]);
      setOlderCursor(page.next_cursor);
    } catch (e) {
      toast(e instanceof ApiError ? e.message : t("loadMoreError"), { tone: "cut" });
    } finally {
      setLoadingMore(false);
    }
  }

  return (
    <AppShell surface="admin">
      <title>{t("pageTitle")}</title>
      <div className="flex flex-col gap-6">
        <PageHeader
          title={t("title")}
          description={t("description")}
          actions={<SearchField value={query} onValueChange={setQuery} label={t("searchLabel")} placeholder={t("searchPlaceholder")} className="w-72" />}
        />
        {first.error ? <LoadError error={first.error} onRetry={reload} /> : null}
        {!first.error && !first.data ? <RowsSkeleton label={t("loading")} /> : null}
        {first.data && events.length === 0 ? <EmptyState icon={ScrollText} title={t("emptyTitle")}>{t("emptyBody")}</EmptyState> : null}
        {events.length > 0 && shown.length === 0 ? (
          <EmptyState icon={ScrollText} title={t("noMatchTitle", { query: query.trim() })} action={<Button variant="secondary" onClick={() => setQuery("")}>{t("clearSearch")}</Button>}>
            {t("noMatchBody")}
          </EmptyState>
        ) : null}
        {shown.length > 0 ? (
          <DataTable caption={t("caption")} columns={[{ label: t("colWhen") }, { label: t("colAdmin") }, { label: t("colAction") }, { label: t("colTarget") }, { label: t("colDetails") }]}>
            {shown.map((event) => (
              <tr key={event.id} className={ROW}>
                <td className={`${CELL} font-mono text-xs whitespace-nowrap text-fg-muted`}>{formatDateTime(event.ts)}</td>
                <td className={CELL}>{event.actor_email}</td>
                <td className={`${CELL} font-mono`}>{event.action}</td>
                <td className={`${CELL} font-mono break-all`}>{event.target}</td>
                <td className={`${CELL} font-mono text-xs break-all text-fg-muted`}>{detailsText(event.details)}</td>
              </tr>
            ))}
          </DataTable>
        ) : null}
        {cursor ? (
          <div>
            <Button variant="secondary" size="sm" loading={loadingMore} onClick={() => void loadMore()}>
              <RotateCw aria-hidden />
              {t("loadMore")}
            </Button>
          </div>
        ) : null}
      </div>
    </AppShell>
  );
}
