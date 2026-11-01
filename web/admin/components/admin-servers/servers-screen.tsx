// Ported from design/v5/src/components/admin-servers/servers-screen.tsx on 2026-10-31.
// The auth filter and sort selector are dropped; the wizard state is owned by the page (hash states became React state).
"use client";

import { Plus, SearchX, ServerOff } from "lucide-react";
import { useState } from "react";

import type { ServerSummary } from "../../lib/admin-types";
import { useT } from "../../lib/i18n";
import { servers as messages } from "../../lib/i18n/messages/servers";
import { Button } from "../ui/button";
import { EmptyState } from "../ui/empty-state";
import { SearchField } from "../ui/input";
import { SegmentedTabs } from "../ui/segmented-tabs";

import { ServersTable } from "./servers-table";

type View = "all" | "attention" | "off";

const needsAttention = (server: ServerSummary) => server.status === "active" && (server.pending_tools > 0 || server.catalog_synced_at === null);

const VIEWS: Record<View, { keep: (server: ServerSummary) => boolean }> = {
  all: { keep: () => true },
  attention: { keep: needsAttention },
  off: { keep: (server) => server.status === "disabled" },
};

const matches = (server: ServerSummary, needle: string) =>
  !needle || `${server.slug} ${server.category}`.toLowerCase().includes(needle);

export interface ServersScreenProps {
  servers: readonly ServerSummary[];
  onAdd: () => void;
}

/** Admin servers list: tabs with counts, search and the table. */
export function ServersScreen({ servers, onAdd }: ServersScreenProps) {
  const t = useT(messages);
  const [view, setView] = useState<View>("all");
  const [query, setQuery] = useState("");

  const needle = query.trim().toLowerCase();
  const matching = servers.filter((server) => matches(server, needle));
  const visible = matching.filter(VIEWS[view].keep);
  const filtered = needle !== "";

  const clearAll = () => {
    setQuery("");
    setView("all");
  };

  return (
    <section aria-labelledby="servers-list-title" className="flex flex-col gap-4">
      <h2 id="servers-list-title" className="sr-only">
        {t("listTitle")}
      </h2>
      <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
        <div className="scrollbar-none -mx-4 overflow-x-auto px-4 max-sm:mask-r-from-90% sm:mx-0 sm:px-0">
          <SegmentedTabs
            label={t("tabsLabel")}
            controls="servers-result"
            value={view}
            onValueChange={setView}
            items={(Object.keys(VIEWS) as View[]).map((v) => ({ value: v, label: t(`view_${v}`), count: matching.filter(VIEWS[v].keep).length }))}
          />
        </div>
        <SearchField value={query} onValueChange={setQuery} label={t("searchLabel")} placeholder={t("searchPlaceholder")} className="min-w-0 lg:w-64" />
      </div>

      <p aria-live="polite" className="sr-only">
        {t(visible.length === 1 ? "count_one" : "count_other", { n: visible.length })}
      </p>

      <div id="servers-result" role="tabpanel" className="flex flex-col gap-3">
        {visible.length > 0 ? (
          <ServersTable servers={visible} />
        ) : (
          <EmptyState
            icon={filtered ? SearchX : ServerOff}
            title={filtered ? t("emptySearchTitle", { query: query.trim() }) : t(`empty_${view}`)}
            action={
              filtered || view !== "all" ? (
                <Button variant="secondary" onClick={clearAll}>
                  {t("clearFilters")}
                </Button>
              ) : (
                <Button variant="primary" onClick={onAdd}>
                  <Plus aria-hidden />
                  {t("addServer")}
                </Button>
              )
            }
          >
            {filtered
              ? t("emptySearchBody")
              : view === "all"
                ? t("emptyNoneBody")
                : t("emptyViewBody")}
          </EmptyState>
        )}
        <p className="text-xs text-fg-muted">
          {t(servers.length === 1 ? "shown_one" : "shown_other", { shown: visible.length, n: servers.length })}
        </p>
      </div>
    </section>
  );
}
