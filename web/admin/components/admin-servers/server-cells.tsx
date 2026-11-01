// Ported from design/v5/src/components/admin-servers/server-cells.tsx on 2026-10-31.
// Connection counts and "last call by tool" are omitted: the servers list carries no per-user or per-call data (plan gap G4).
"use client";

import { KeyRound, ShieldCheck } from "lucide-react";

import { credentialSummary, serverStatusLabel, type AuthKind, type ServerSummary } from "../../lib/admin-types";
import { formatDateTime } from "../../lib/format";
import { getLocale, translate, useLocale, useT } from "../../lib/i18n";
import { servers } from "../../lib/i18n/messages/servers";
import { Badge, Pill } from "../ui/badge";

/** What the upstream accepts. Header and OAuth read as neutral facts; "none" keeps the product pill. */
export function AuthBadge({ kind, credentialEnv }: { kind: AuthKind; credentialEnv?: string | null }) {
  const t = useT(servers);
  if (kind === "none") return <Pill kind="open" />;
  if (credentialEnv) return <Badge icon={KeyRound}>{t("perUserToken")}</Badge>;
  return kind === "oauth" ? <Badge icon={ShieldCheck}>{t("authOAuth")}</Badge> : <Badge icon={KeyRound}>{t("authHeader")}</Badge>;
}

export function StatusBadge({ status }: { status: string }) {
  useLocale();
  return <Badge tone={status === "active" ? "ok" : "neutral"}>{serverStatusLabel(status)}</Badge>;
}

export function CredentialCell({ server }: { server: ServerSummary }) {
  useLocale();
  if (server.auth_kind === "none") return <span className="text-xs text-fg-muted">{credentialSummary(server)}</span>;
  return server.has_shared_connection ? <Pill kind="shared" /> : <span className="text-xs text-fg-muted">{credentialSummary(server)}</span>;
}

export function LastUsed({ iso }: { iso: string | null | undefined }) {
  const t = useT(servers);
  if (!iso) return <span className="text-[13px] text-fg-muted">{t("noUsage")}</span>;
  return <span className="text-[13px] text-fg">{formatDateTime(iso)}</span>;
}

/** "1 tool" / "3 tools". Singular only for exactly 1: pt-BR plural rules would also treat 0 as singular. */
export const toolsLabel = (count: number) => translate(getLocale(), servers, count === 1 ? "tools_one" : "tools_other", { n: count });

/** Where a server's detail page lives: static export has no dynamic segments, so the slug is a query. */
export const serverHref = (slug: string) => `/admin/servers/view/?slug=${encodeURIComponent(slug)}`;
