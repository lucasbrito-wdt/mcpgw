// Ported from design/v5/src/components/admin-servers/servers-table.tsx on 2026-10-31.
// Column sorting is dropped (the API orders the list); every cell is contract data.
"use client";

import Link from "next/link";

import type { ServerSummary } from "../../lib/admin-types";
import { useT } from "../../lib/i18n";
import { servers as messages } from "../../lib/i18n/messages/servers";
import { ServiceLogo } from "../brand/ServiceLogo";
import { Badge } from "../ui/badge";

import { AuthBadge, CredentialCell, LastUsed, StatusBadge, serverHref, toolsLabel } from "./server-cells";

export interface ServersTableProps {
  servers: readonly ServerSummary[];
}

const ROW_BORDER = "border-t border-line first:border-t-0";
const HEAD = "px-3 py-2 text-xs font-medium text-fg-muted";

function ServerName({ server }: { server: ServerSummary }) {
  const t = useT(messages);
  return (
    <Link href={serverHref(server.slug)} className="focus-ring -m-1 flex min-w-0 items-center gap-3 rounded-control p-1 hover:bg-white/6">
      <ServiceLogo slug={server.slug} name={server.slug} variant="catalog" size="sm" />
      <span className="min-w-0 leading-tight">
        <span className="block truncate font-mono text-[13px] font-semibold text-fg">{server.slug}</span>
        <span className="block truncate text-[11px] text-fg-muted">{server.category || t("noCategory")}</span>
      </span>
    </Link>
  );
}

function Pending({ count }: { count: number }) {
  const t = useT(messages);
  if (count === 0) return null;
  return (
    <Link href="/admin/review/" className="focus-ring rounded-[4px]" aria-label={t("pendingAria", { n: count })}>
      <Badge tone="warn">{t("inReview", { n: count })}</Badge>
    </Link>
  );
}

/** Dense server table from md up; below md every row is a card, so nothing scrolls sideways. */
export function ServersTable({ servers }: ServersTableProps) {
  const t = useT(messages);
  return (
    <>
      <div className="hidden overflow-hidden rounded-card border border-line bg-panel/85 md:block">
        <table className="w-full table-fixed border-collapse text-left">
          <caption className="sr-only">{t("tableCaption")}</caption>
          <colgroup>
            <col className="w-[24%]" />
            <col className="w-[13%]" />
            <col className="w-[19%]" />
            <col className="w-[16%]" />
            <col className="w-[10%]" />
            <col className="w-[18%]" />
          </colgroup>
          <thead className="border-b border-line-strong">
            <tr>
              <th scope="col" className={HEAD}>{t("col_server")}</th>
              <th scope="col" className={HEAD}>{t("col_auth")}</th>
              <th scope="col" className={HEAD}>{t("col_credential")}</th>
              <th scope="col" className={HEAD}>{t("col_tools")}</th>
              <th scope="col" className={HEAD}>{t("col_status")}</th>
              <th scope="col" className={HEAD}>{t("col_lastUsed")}</th>
            </tr>
          </thead>
          <tbody>
            {servers.map((server) => (
              <tr key={server.slug} className={`${ROW_BORDER} h-14 transition-colors duration-150 ease-switch hover:bg-white/4`}>
                <th scope="row" className="px-3 py-2 text-left font-normal">
                  <ServerName server={server} />
                </th>
                <td className="px-3 py-2"><AuthBadge kind={server.auth_kind} credentialEnv={server.credential_env} /></td>
                <td className="px-3 py-2"><CredentialCell server={server} /></td>
                <td className="px-3 py-2">
                  <span className="flex flex-wrap items-center gap-2 text-[13px]">
                    <span className="font-mono tabular-nums text-fg">{server.active_tools}</span>
                    <Pending count={server.pending_tools} />
                  </span>
                </td>
                <td className="px-3 py-2"><StatusBadge status={server.status} /></td>
                <td className="px-3 py-2"><LastUsed iso={server.last_used_at} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <ul className="flex flex-col gap-2 md:hidden" aria-label={t("tableCaption")}>
        {servers.map((server) => (
          <li key={server.slug} className="flex flex-col gap-3 rounded-card border border-line bg-panel/85 p-3.5">
            <div className="flex items-start justify-between gap-3">
              <ServerName server={server} />
              <StatusBadge status={server.status} />
            </div>
            <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
              <AuthBadge kind={server.auth_kind} credentialEnv={server.credential_env} />
              <CredentialCell server={server} />
            </div>
            <div className="flex items-end justify-between gap-3 border-t border-line pt-3 text-xs text-fg-muted">
              <span className="flex items-center gap-2">
                {toolsLabel(server.active_tools)}
                <Pending count={server.pending_tools} />
              </span>
              <LastUsed iso={server.last_used_at} />
            </div>
          </li>
        ))}
      </ul>
    </>
  );
}
