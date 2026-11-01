"use client";

import { MonitorSmartphone } from "lucide-react";
import { useState } from "react";

import { CELL, DataTable, ROW } from "../../../components/admin-logs/data-table";
import { RevokeDialog } from "../../../components/admin/RevokeDialog";
import { LoadError, RowsSkeleton } from "../../../components/admin-servers/load-states";
import { AppShell } from "../../../components/shell/AppShell";
import { PageHeader } from "../../../components/shell/PageHeader";
import { Badge } from "../../../components/ui/badge";
import { Button } from "../../../components/ui/button";
import { EmptyState } from "../../../components/ui/empty-state";
import { SegmentedTabs } from "../../../components/ui/segmented-tabs";
import { useToast } from "../../../components/ui/toast";
import { useLoad, type Paged } from "../../../lib/admin-types";
import { api, ApiError } from "../../../lib/api";
import { formatDateTime, levelLabel } from "../../../lib/format";
import { useT, type Translator } from "../../../lib/i18n";
import { adminClients } from "../../../lib/i18n/messages/adminClients";
import type { AccessLevel } from "../../../lib/types";

interface OAuthClient {
  client_id: string;
  name: string;
  registration_kind: string;
  redirect_uris: string[];
  created_at: string;
  active_consents: number;
}

interface Consent {
  id: string;
  email: string;
  client_id: string;
  client_name: string;
  server: string;
  level: string;
  created_at: string;
  expires_at: string | null;
  revoked_at: string | null;
}

interface Grant {
  family_id: string;
  email: string;
  client_id: string;
  scope: string;
  created_at: string;
  last_used_at: string | null;
  expires_at: string | null;
}

type View = "clients" | "consents" | "grants";
const VIEWS: readonly View[] = ["clients", "consents", "grants"];
const VIEW_KEY = { clients: "viewClients", consents: "viewConsents", grants: "viewGrants" } as const;
const EMPTY_KEY = { clients: "emptyClients", consents: "emptyConsents", grants: "emptyGrants" } as const;
type T = Translator<typeof adminClients.en>;
const kindLabel = (t: T, kind: string) => (kind === "cimd" ? t("kind_cimd") : kind === "dcr" ? t("kind_dcr") : kind);
const LIST_LIMIT = 200;

/** What a pending revoke needs: the request path and the sentence the person confirms. */
interface Pending {
  path: string;
  client: string;
  /** Toast after it worked. */
  done: string;
}

const dateOrDash = (iso: string | null) => (iso ? formatDateTime(iso) : "-");
const levelText = (level: string) => (level in { none: 1, read: 1, write: 1, destructive: 1 } ? levelLabel(level as AccessLevel) : level);

function ClientsTable({ clients }: { clients: readonly OAuthClient[] }) {
  const t = useT(adminClients);
  return (
    <DataTable caption={t("clientsCaption")} columns={[{ label: t("colClient") }, { label: t("colRegistration") }, { label: t("colActiveConsents") }, { label: t("colCreated") }]}>
      {clients.map((client) => (
        <tr key={client.client_id} className={ROW}>
          <td className={CELL}>
            <span className="block font-semibold">{client.name || t("unnamedClient")}</span>
            <span className="block max-w-80 truncate font-mono text-[11px] text-fg-muted" title={client.client_id}>{client.client_id}</span>
          </td>
          <td className={CELL}><Badge>{kindLabel(t, client.registration_kind)}</Badge></td>
          <td className={`${CELL} font-mono tabular-nums`}>{client.active_consents}</td>
          <td className={`${CELL} text-fg-muted`}>{formatDateTime(client.created_at)}</td>
        </tr>
      ))}
    </DataTable>
  );
}

function ConsentsTable({ consents, onRevoke }: { consents: readonly Consent[]; onRevoke: (consent: Consent) => void }) {
  const t = useT(adminClients);
  return (
    <DataTable caption={t("consentsCaption")} columns={[{ label: t("colPerson") }, { label: t("colClient") }, { label: t("colServerLevel") }, { label: t("colGivenAt") }, { label: t("colActions"), hidden: true }]}>
      {consents.map((consent) => (
        <tr key={consent.id} className={ROW}>
          <td className={CELL}>{consent.email}</td>
          <td className={CELL}>{consent.client_name || consent.client_id}</td>
          <td className={CELL}>
            <span className="font-mono">{consent.server}</span> <span className="text-fg-muted">{levelText(consent.level)}</span>
          </td>
          <td className={`${CELL} text-fg-muted`}>{formatDateTime(consent.created_at)}</td>
          <td className={`${CELL} text-right`}>
            {consent.revoked_at ? (
              <span className="text-xs text-fg-muted">{t("revoked")}</span>
            ) : (
              <Button variant="ghost" size="sm" className="text-cut hover:text-cut" aria-label={t("revokeConsentAria", { email: consent.email, client: consent.client_name || consent.client_id })} onClick={() => onRevoke(consent)}>
                {t("revoke")}
              </Button>
            )}
          </td>
        </tr>
      ))}
    </DataTable>
  );
}

function GrantsTable({ grants, onRevoke }: { grants: readonly Grant[]; onRevoke: (grant: Grant) => void }) {
  const t = useT(adminClients);
  return (
    <DataTable caption={t("grantsCaption")} columns={[{ label: t("colPerson") }, { label: t("colClient") }, { label: t("colLastUsed") }, { label: t("colExpires") }, { label: t("colActions"), hidden: true }]}>
      {grants.map((grant) => (
        <tr key={grant.family_id} className={ROW}>
          <td className={CELL}>{grant.email}</td>
          <td className={CELL}><span className="block max-w-72 truncate" title={grant.client_id}>{grant.client_id}</span></td>
          <td className={`${CELL} text-fg-muted`}>{dateOrDash(grant.last_used_at)}</td>
          <td className={`${CELL} text-fg-muted`}>{dateOrDash(grant.expires_at)}</td>
          <td className={`${CELL} text-right`}>
            <Button variant="ghost" size="sm" className="text-cut hover:text-cut" aria-label={t("revokeGrantAria", { email: grant.email, client: grant.client_id })} onClick={() => onRevoke(grant)}>
              {t("revoke")}
            </Button>
          </td>
        </tr>
      ))}
    </DataTable>
  );
}

export default function AdminClientsPage() {
  const t = useT(adminClients);
  const toast = useToast();
  const [view, setView] = useState<View>("clients");
  const clients = useLoad<Paged<OAuthClient>>(view === "clients" ? `/clients?limit=${LIST_LIMIT}` : null);
  const consents = useLoad<Paged<Consent>>(view === "consents" ? `/consents?limit=${LIST_LIMIT}` : null);
  const grants = useLoad<Paged<Grant>>(view === "grants" ? `/grants?limit=${LIST_LIMIT}` : null);
  const [pending, setPending] = useState<Pending | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();

  const current = view === "clients" ? clients : view === "consents" ? consents : grants;
  const items = current.data?.items ?? [];

  const ask = (next: Pending) => {
    setError(undefined);
    setPending(next);
  };

  async function revoke() {
    if (!pending) return;
    setBusy(true);
    setError(undefined);
    try {
      await api<void>("admin", pending.path, { method: "DELETE" });
      toast(pending.done, { tone: "ok" });
      setPending(null);
      current.reload();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t("revokeError"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <AppShell surface="admin">
      <title>{t("pageTitle")}</title>
      <div className="flex flex-col gap-6">
        <PageHeader title={t("title")} description={t("description")} />
        <SegmentedTabs label={t("showLabel")} controls="clients-panel" value={view} onValueChange={setView} items={VIEWS.map((v) => ({ value: v, label: t(VIEW_KEY[v]) }))} />
        <div id="clients-panel" className="flex flex-col gap-3">
          {current.error ? <LoadError error={current.error} onRetry={current.reload} /> : null}
          {!current.error && !current.data ? <RowsSkeleton label={t("loading")} /> : null}
          {current.data && items.length === 0 ? (
            <EmptyState icon={MonitorSmartphone} title={t(EMPTY_KEY[view])}>
              {t("emptyBody")}
            </EmptyState>
          ) : null}
          {view === "clients" && clients.data && items.length > 0 ? <ClientsTable clients={clients.data.items} /> : null}
          {view === "consents" && consents.data && items.length > 0 ? (
            <ConsentsTable
              consents={consents.data.items}
              onRevoke={(c) => ask({ path: `/consents/${encodeURIComponent(c.id)}`, client: c.client_name || c.client_id, done: t("revokedToast", { client: c.client_name || c.client_id }) })}
            />
          ) : null}
          {view === "grants" && grants.data && items.length > 0 ? (
            <GrantsTable grants={grants.data.items} onRevoke={(g) => ask({ path: `/grants/${encodeURIComponent(g.family_id)}`, client: g.client_id, done: t("revokedToast", { client: g.client_id }) })} />
          ) : null}
          {current.data?.next_cursor ? <p className="text-xs text-fg-muted">{t("truncated", { n: LIST_LIMIT })}</p> : null}
        </div>
      </div>
      <RevokeDialog
        open={pending !== null}
        title={pending ? t("dialogTitle", { client: pending.client }) : t("dialogTitleFallback")}
        description={t("dialogDescription")}
        busy={busy}
        error={error}
        onConfirm={() => void revoke()}
        onClose={() => setPending(null)}
      />
    </AppShell>
  );
}
