// Ported from design/v5/src/components/admin-connections/connections-table.tsx on 2026-10-31.
// Personal rows only; the shared key of each server lives in shared-table.tsx.
import { useT, type Translator } from "../../lib/i18n";
import { adminConnections } from "../../lib/i18n/messages/adminConnections";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";

import { ExpiryText, KeyBadge, lastUsedText, RowCheckbox, ServerCell, StateCell } from "./connection-cells";
import { MAX_BULK, rowId, routeState } from "./connections-model";
import type { UserConnection } from "./types";

export interface ConnectionsTableProps {
  rows: readonly UserConnection[];
  /** Auth kind per server slug, to word an empty expiry. */
  authKinds: ReadonlyMap<string, string>;
  selected: ReadonlySet<string>;
  onToggle: (id: string) => void;
  onToggleAll: () => void;
  onRevoke: (connection: UserConnection) => void;
}

type T = Translator<typeof adminConnections.en>;
const describe = (t: T, c: UserConnection) => (c.profile === "default" ? t("rowDescribe", { email: c.email, server: c.server }) : t("rowDescribeProfile", { email: c.email, server: c.server, profile: c.profile }));
const HEAD = "px-3 py-2 text-xs font-medium text-fg-muted";
/** A key a person can still lose: an admin-revoked row has nothing left to revoke. */
const canRevoke = (c: UserConnection) => c.status !== "revoked_by_admin";

function PersonCell({ c }: { c: UserConnection }) {
  const t = useT(adminConnections);
  return (
    <span className="block min-w-0 leading-tight">
      <span className="block truncate text-[13px] font-semibold text-fg">{c.email}</span>
      <span className="block truncate font-mono text-[11px] text-fg-muted">{c.account_label ?? t("unnamedAccount")}</span>
    </span>
  );
}

function RevokeButton({ c, onRevoke, className }: { c: UserConnection; onRevoke: ConnectionsTableProps["onRevoke"]; className?: string }) {
  const t = useT(adminConnections);
  if (!canRevoke(c)) return <span className="text-xs text-fg-muted">{t("alreadyRevoked")}</span>;
  return (
    <Button variant="ghost" size="sm" onClick={() => onRevoke(c)} aria-label={t("revokeAria", { desc: describe(t, c) })} className={cn("text-cut hover:text-cut", className)}>
      {t("revoke")}
    </Button>
  );
}

/** Dense table from md up; below md every row is a card, so nothing scrolls sideways. */
export function ConnectionsTable({ rows, authKinds, selected, onToggle, onToggleAll, onRevoke }: ConnectionsTableProps) {
  const t = useT(adminConnections);
  const full = selected.size >= MAX_BULK;
  const selectable = rows.filter(canRevoke);
  const allSelected = selectable.length > 0 && selectable.every((r) => selected.has(rowId(r)));
  const some = selectable.some((r) => selected.has(rowId(r)));
  const lockedOut = (c: UserConnection) => !canRevoke(c) || (full && !selected.has(rowId(c)));

  return (
    <>
      <div className="hidden overflow-hidden rounded-card border border-line bg-panel/85 md:block">
        <table className="w-full table-fixed border-collapse text-left">
          <caption className="sr-only">{t("caption")}</caption>
          <colgroup>
            <col className="w-12" />
            <col className="w-[22%]" />
            <col className="w-[16%]" />
            <col className="w-[20%]" />
            <col className="w-[14%]" />
            <col className="w-[14%]" />
            <col className="w-28" />
          </colgroup>
          <thead className="border-b border-line-strong">
            <tr>
              <th scope="col" className="py-2 pr-0 pl-4">
                <RowCheckbox checked={allSelected} indeterminate={some && !allSelected} onChange={onToggleAll} label={t("selectAll")} />
              </th>
              <th scope="col" className={HEAD}>{t("colPerson")}</th>
              <th scope="col" className={HEAD}>{t("colServer")}</th>
              <th scope="col" className={HEAD}>{t("colState")}</th>
              <th scope="col" className={HEAD}>{t("colLastUsed")}</th>
              <th scope="col" className={HEAD}>{t("colExpires")}</th>
              <th scope="col" className={HEAD}>
                <span className="sr-only">{t("colActions")}</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map((c) => {
              const id = rowId(c);
              return (
                <tr key={id} data-selected={selected.has(id) || undefined} className="border-t border-line transition-colors duration-150 ease-switch first:border-t-0 hover:bg-white/3 data-selected:bg-signal-wash/40 motion-reduce:transition-none">
                  <td className="py-3 pr-0 pl-4">
                    <RowCheckbox checked={selected.has(id)} disabled={lockedOut(c)} onChange={() => onToggle(id)} label={t("selectRow", { desc: describe(t, c) })} />
                  </td>
                  <td className="px-3 py-3"><PersonCell c={c} /></td>
                  <td className="px-3 py-3"><ServerCell slug={c.server} profile={c.profile} /></td>
                  <td className="px-3 py-3"><StateCell keyKind="personal" route={routeState(c.status)} /></td>
                  <td className="px-3 py-3 text-xs text-fg-muted">{lastUsedText(c.last_used_at)}</td>
                  <td className="px-3 py-3"><ExpiryText iso={c.expires_at} authKind={authKinds.get(c.server)} gone={c.status === "expired"} /></td>
                  <td className="px-3 py-3 text-right"><RevokeButton c={c} onRevoke={onRevoke} /></td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      <ul className="flex flex-col gap-2.5 md:hidden">
        {rows.map((c) => {
          const id = rowId(c);
          return (
            <li key={id} data-selected={selected.has(id) || undefined} className="rounded-card border border-line bg-panel/85 p-3.5 data-selected:border-signal/60 data-selected:bg-signal-wash/40">
              <div className="flex items-start gap-3">
                <RowCheckbox checked={selected.has(id)} disabled={lockedOut(c)} onChange={() => onToggle(id)} label={t("selectRow", { desc: describe(t, c) })} />
                <div className="min-w-0 flex-1"><PersonCell c={c} /></div>
                <KeyBadge kind="personal" />
              </div>
              <div className="mt-3"><ServerCell slug={c.server} profile={c.profile} /></div>
              <div className="mt-3 border-t border-line pt-3"><StateCell keyKind="personal" route={routeState(c.status)} /></div>
              <div className="mt-2 flex items-center justify-between gap-3">
                <p className="text-xs text-fg-muted">
                  {t("usedLabel")} {lastUsedText(c.last_used_at)}. <ExpiryText iso={c.expires_at} authKind={authKinds.get(c.server)} gone={c.status === "expired"} />
                </p>
                <RevokeButton c={c} onRevoke={onRevoke} className="-mr-2" />
              </div>
            </li>
          );
        })}
      </ul>
    </>
  );
}
