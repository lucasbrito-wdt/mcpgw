// Ported from design/v5/src/components/admin-connections/connections-screen.tsx on 2026-10-31.
// Hash states became React state; rows come from `GET /connections` (plan gap G4: no mock-only columns).
"use client";

import { CircleCheck, KeyRound, SearchX, ShieldCheck, X } from "lucide-react";
import Link from "next/link";
import { useMemo, useState } from "react";

import { api, ApiError } from "../../lib/api";
import { useLoad } from "../../lib/admin-types";
import { useT } from "../../lib/i18n";
import { adminConnections } from "../../lib/i18n/messages/adminConnections";
import { ReauthCancelled, useReauth } from "../admin/ReauthDialog";
import { RevokeDialog } from "../admin/RevokeDialog";
import { LoadError, RowsSkeleton } from "../admin-servers/load-states";
import { Button } from "../ui/button";
import { buttonVariants } from "../ui/button-variants";
import { EmptyState } from "../ui/empty-state";
import { SearchField } from "../ui/input";
import { SegmentedTabs } from "../ui/segmented-tabs";
import { useToast } from "../ui/toast";

import { BulkBar } from "./bulk-bar";
import { isProblemRow, isProblemServer, matchesQuery, MAX_BULK, personalRows, rowId, tabLabel, TABS, targetId, type RevokeTarget, type Tab } from "./connections-model";
import { ConnectionsTable } from "./connections-table";
import { ConnectionRevokeDialog } from "./revoke-dialog";
import { SharedTable } from "./shared-table";
import type { ConnectionServer, ConnectionsResponse, UserConnection } from "./types";

const NO_IDS: ReadonlySet<string> = new Set();
const PAGE_LIMIT = 200;

const EMPTY_KEYS = {
  personal: { title: "emptyPersonalTitle", text: "emptyPersonalBody" },
  shared: { title: "emptySharedTitle", text: "emptySharedBody" },
  problem: { title: "emptyProblemTitle", text: "emptyProblemBody" },
} as const;

interface Removal {
  slug: string;
}

/** Admin connections: tabs with counts, search, selection up to 100, and the revoke dialog that asks for a reason. */
export function ConnectionsScreen() {
  const t = useT(adminConnections);
  const toast = useToast();
  const data = useLoad<ConnectionsResponse>(`/connections?limit=${PAGE_LIMIT}`);
  const [tab, setTab] = useState<Tab>("personal");
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<ReadonlySet<string>>(NO_IDS);
  const [targets, setTargets] = useState<readonly RevokeTarget[] | null>(null);
  const [removal, setRemoval] = useState<Removal | null>(null);
  const reauth = useReauth();
  const [removing, setRemoving] = useState(false);
  const [removeError, setRemoveError] = useState<string | undefined>();
  const [notice, setNotice] = useState<string | null>(null);

  const servers = useMemo(() => data.data?.servers ?? [], [data.data]);
  const personal = useMemo(() => personalRows(data.data?.items ?? []), [data.data]);
  const authKinds = useMemo(() => new Map(servers.map((s) => [s.slug, s.auth_kind])), [servers]);

  const matchingPersonal = personal.filter((c) => matchesQuery(c, query));
  const matchingServers = servers.filter((s) => !query.trim() || s.slug.toLowerCase().includes(query.trim().toLowerCase()));
  const problemRows = matchingPersonal.filter(isProblemRow);
  const problemServers = matchingServers.filter(isProblemServer);
  const counts: Record<Tab, number> = { personal: matchingPersonal.length, shared: matchingServers.length, problem: problemRows.length + problemServers.length };
  const sharedList = tab === "shared" ? matchingServers : tab === "problem" ? problemServers : [];
  const visiblePersonal = tab === "problem" ? problemRows : matchingPersonal;
  const searching = query.trim() !== "";

  const byId = (id: string) => personal.find((c) => rowId(c) === id);
  const revokable = visiblePersonal.filter((c) => c.status !== "revoked_by_admin");

  function toggle(id: string) {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else if (next.size < MAX_BULK) next.add(id);
      return next;
    });
  }

  function toggleAll() {
    setSelected((current) => {
      const next = new Set(current);
      if (revokable.every((c) => next.has(rowId(c)))) revokable.forEach((c) => next.delete(rowId(c)));
      else for (const c of revokable) if (next.size < MAX_BULK) next.add(rowId(c));
      return next;
    });
  }

  const personalTarget = (c: UserConnection): RevokeTarget => ({ kind: "personal", server: c.server, userId: c.user_id, profile: c.profile, email: c.email });

  function revoked(ids: readonly string[]) {
    const first = ids.length === 1 ? targets?.find((t) => targetId(t) === ids[0]) : undefined;
    setNotice(
      first
        ? t("noticeOne", { who: first.kind === "shared" ? t("whoShared") : first.email, server: first.server })
        : t.plural("noticeMany", ids.length),
    );
    setSelected((current) => new Set(Array.from(current).filter((id) => !ids.includes(id))));
    setTargets((current) => {
      const left = (current ?? []).filter((t) => !ids.includes(targetId(t)));
      return left.length > 0 ? left : null;
    });
    data.reload();
  }

  async function removeShared() {
    if (!removal) return;
    setRemoving(true);
    setRemoveError(undefined);
    try {
      await reauth.run(() => api<void>("admin", `/servers/${encodeURIComponent(removal.slug)}/shared`, { method: "DELETE" }));
      toast(t("removedToast", { slug: removal.slug }), { tone: "ok" });
      setRemoval(null);
      data.reload();
    } catch (e) {
      if (!(e instanceof ReauthCancelled)) setRemoveError(e instanceof ApiError ? e.message : t("removeError"));
    } finally {
      setRemoving(false);
    }
  }

  if (data.error) return <LoadError error={data.error} onRetry={data.reload} />;
  if (!data.data) return <RowsSkeleton label={t("loading")} />;

  const empty = EMPTY_KEYS[tab];
  const nothing = tab === "shared" ? matchingServers.length === 0 : tab === "personal" ? matchingPersonal.length === 0 : counts.problem === 0;
  const total = tab === "shared" ? servers.length : personal.length;
  const shownCount = tab === "shared" ? matchingServers.length : visiblePersonal.length;

  return (
    <section aria-labelledby="connections-title" className="flex flex-col gap-4">
      <h2 id="connections-title" className="sr-only">
        {t("listTitle")}
      </h2>

      <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
        <div className="scrollbar-none -mx-4 overflow-x-auto px-4 max-sm:mask-r-from-90% sm:mx-0 sm:px-0">
          <SegmentedTabs
            label={t("tabsLabel")}
            controls="connections-result"
            value={tab}
            onValueChange={(next) => {
              setTab(next);
              setSelected(NO_IDS);
            }}
            items={TABS.map((tab) => ({ value: tab, label: tabLabel(tab), count: counts[tab] }))}
          />
        </div>
        <SearchField value={query} onValueChange={setQuery} label={t("searchLabel")} placeholder={t("searchPlaceholder")} shortcut={false} className="lg:w-72" />
      </div>

      <div aria-live="polite" className="empty:hidden">
        {notice ? (
          <p role="status" className="flex items-start gap-2.5 rounded-card border border-ok/30 bg-ok-wash p-3 text-sm text-fg">
            <CircleCheck className="mt-0.5 size-4 shrink-0 text-ok" aria-hidden />
            <span className="min-w-0 flex-1">{notice}</span>
            <button type="button" onClick={() => setNotice(null)} aria-label={t("dismiss")} className="focus-ring -m-1 grid size-7 shrink-0 cursor-pointer place-items-center rounded-control text-fg-muted transition-colors duration-150 ease-switch hover:bg-white/10 hover:text-fg active:bg-white/15">
              <X className="size-4" aria-hidden />
            </button>
          </p>
        ) : null}
      </div>

      <p aria-live="polite" className="sr-only">
        {shownCount === 1 ? t("connectionOne") : t("connectionMany", { n: shownCount })}
      </p>

      <div id="connections-result" className="flex flex-col gap-3">
        {!nothing ? (
          <>
            {sharedList.length > 0 ? (
              <SharedTable
                servers={sharedList}
                onRevoke={(s: ConnectionServer) => setTargets([{ kind: "shared", server: s.slug }])}
                onRemove={(s: ConnectionServer) => {
                  setRemoveError(undefined);
                  setRemoval({ slug: s.slug });
                }}
              />
            ) : null}
            {tab !== "shared" && visiblePersonal.length > 0 ? (
              <ConnectionsTable rows={visiblePersonal} authKinds={authKinds} selected={selected} onToggle={toggle} onToggleAll={toggleAll} onRevoke={(c) => setTargets([personalTarget(c)])} />
            ) : null}
          </>
        ) : (
          <EmptyState
            icon={searching ? SearchX : tab === "problem" ? ShieldCheck : KeyRound}
            title={searching ? t("noMatchTitle", { query: query.trim() }) : t(empty.title)}
            action={
              searching ? (
                <Button variant="secondary" onClick={() => setQuery("")}>
                  {t("clearSearch")}
                </Button>
              ) : tab === "shared" ? (
                <Link href="/admin/servers/" className={buttonVariants({ variant: "secondary" })}>
                  {t("viewServers")}
                </Link>
              ) : undefined
            }
          >
            {searching ? t("noMatchBody") : t(empty.text)}
          </EmptyState>
        )}
        <p className="text-xs text-fg-muted">
          {t(tab === "shared" ? "footerServers" : "footerConnections", { shown: shownCount, total })}
          {data.data.next_cursor && tab !== "shared" ? `. ${t("footerMore", { limit: PAGE_LIMIT })}` : ""}
        </p>
      </div>

      <BulkBar count={selected.size} onClear={() => setSelected(NO_IDS)} onRevoke={() => setTargets(Array.from(selected).flatMap((id) => { const c = byId(id); return c ? [personalTarget(c)] : []; }))} />

      <ConnectionRevokeDialog targets={targets} servers={servers} onClose={() => setTargets(null)} onRevoked={revoked} />

      <RevokeDialog
        open={removal !== null}
        title={t("removeTitle")}
        description={removal ? t("removeDescription", { slug: removal.slug }) : undefined}
        confirmLabel={t("remove")}
        busy={removing}
        error={removeError}
        onConfirm={() => void removeShared()}
        onClose={() => setRemoval(null)}
      />
      {reauth.dialog}
    </section>
  );
}
