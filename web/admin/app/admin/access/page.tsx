"use client";

import { SearchX, Table2 } from "lucide-react";
import { useMemo, useState } from "react";

import { CeilingMatrix, parseLevel, ceilingKey, type CeilingState, type CeilingUser } from "../../../components/admin/CeilingMatrix";
import { LoadError, RowsSkeleton } from "../../../components/admin-servers/load-states";
import { AppShell } from "../../../components/shell/AppShell";
import { PageHeader } from "../../../components/shell/PageHeader";
import { EmptyState } from "../../../components/ui/empty-state";
import { SearchField } from "../../../components/ui/input";
import { useLoad, type Paged, type ServerSummary } from "../../../lib/admin-types";
import { useT } from "../../../lib/i18n";
import { adminAccess } from "../../../lib/i18n/messages/adminAccess";

interface CeilingItem {
  user_id: string;
  email: string;
  server: string;
  level: string;
}

export default function AdminAccessPage() {
  const t = useT(adminAccess);
  const users = useLoad<Paged<CeilingUser>>("/users?limit=200");
  const servers = useLoad<Paged<ServerSummary>>("/servers?limit=200");
  const ceilings = useLoad<{ items: CeilingItem[] }>("/ceilings");
  const [query, setQuery] = useState("");

  const initial = useMemo<CeilingState>(() => {
    const state: CeilingState = {};
    for (const item of ceilings.data?.items ?? []) {
      const level = parseLevel(item.level);
      if (level !== "none") state[ceilingKey(item.user_id, item.server)] = level;
    }
    return state;
  }, [ceilings.data]);

  const error = users.error ?? servers.error ?? ceilings.error;
  const userList = users.data?.items;
  const serverList = servers.data?.items;
  const ready = userList !== undefined && serverList !== undefined && ceilings.data !== undefined;
  const needle = query.trim().toLowerCase();
  const visibleUsers = (userList ?? []).filter((user) => !needle || user.email.toLowerCase().includes(needle));
  const reload = () => {
    users.reload();
    servers.reload();
    ceilings.reload();
  };

  return (
    <AppShell surface="admin">
      <title>{t("pageTitle")}</title>
      <div className="flex flex-col gap-6">
        <PageHeader
          title={t("title")}
          description={t("description")}
          actions={<SearchField value={query} onValueChange={setQuery} label={t("searchLabel")} placeholder={t("searchPlaceholder")} className="w-64" />}
        />
        {error ? <LoadError error={error} onRetry={reload} /> : null}
        {!error && !ready ? <RowsSkeleton label={t("loading")} /> : null}
        {ready && userList.length === 0 ? <EmptyState icon={Table2} title={t("noUsersTitle")}>{t("noUsersBody")}</EmptyState> : null}
        {ready && userList.length > 0 && serverList.length === 0 ? <EmptyState icon={Table2} title={t("noServersTitle")}>{t("noServersBody")}</EmptyState> : null}
        {ready && userList.length > 0 && serverList.length > 0 && visibleUsers.length === 0 ? (
          <EmptyState icon={SearchX} title={t("noMatchTitle", { query: query.trim() })}>{t("noMatchBody")}</EmptyState>
        ) : null}
        {/* Stays mounted while a search hides every row, so edits already made are not lost. */}
        {ready && userList.length > 0 && serverList.length > 0 ? (
          <div hidden={visibleUsers.length === 0}>
            <CeilingMatrix users={visibleUsers} servers={serverList.map((s) => s.slug)} initial={initial} />
          </div>
        ) : null}
      </div>
    </AppShell>
  );
}
