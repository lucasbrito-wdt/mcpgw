// New in 4.3 (spec "Screens": built from the same cells). One row per server with its shared connection.
import { useT, type Translator } from "../../lib/i18n";
import { adminConnections } from "../../lib/i18n/messages/adminConnections";
import { Button } from "../ui/button";

import { ExpiryText, KeyBadge, lastUsedText, ServerCell, StateCell } from "./connection-cells";
import { routeState } from "./connections-model";
import type { ConnectionServer } from "./types";

export interface SharedTableProps {
  servers: readonly ConnectionServer[];
  onRevoke: (server: ConnectionServer) => void;
  onRemove: (server: ConnectionServer) => void;
}

const HEAD = "px-3 py-2 text-xs font-medium text-fg-muted";

type T = Translator<typeof adminConnections.en>;

/** "Shared: •••• 9f2c, by admin@..." or "No shared connection". The key itself never reaches the browser. */
function sharedSummary(server: ConnectionServer, t: T): string {
  const shared = server.shared;
  if (!shared) return t("sharedNone");
  const key = shared.last4 ? `•••• ${shared.last4}` : "OAuth";
  return shared.connected_by_email ? t("sharedSummaryBy", { key, email: shared.connected_by_email }) : t("sharedSummary", { key });
}

const usingText = (count: number, t: T): string => (count === 1 ? t("usingOne") : t("usingMany", { n: count }));

function Actions({ server, onRevoke, onRemove }: { server: ConnectionServer } & Pick<SharedTableProps, "onRevoke" | "onRemove">) {
  const t = useT(adminConnections);
  const shared = server.shared;
  if (!shared) return null;
  return (
    <span className="flex flex-wrap items-center justify-end gap-1">
      {shared.status === "active" ? (
        <Button variant="ghost" size="sm" onClick={() => onRevoke(server)} aria-label={t("revokeSharedAria", { slug: server.slug })} className="text-cut hover:text-cut">
          {t("revokeShared")}
        </Button>
      ) : null}
      <Button variant="ghost" size="sm" onClick={() => onRemove(server)} aria-label={t("removeSharedAria", { slug: server.slug })}>
        {t("remove")}
      </Button>
    </span>
  );
}

function State({ server }: { server: ConnectionServer }) {
  const t = useT(adminConnections);
  if (!server.shared) return <span className="text-xs text-fg-muted">{t("noKey")}</span>;
  return <StateCell keyKind="shared" route={routeState(server.shared.status)} />;
}

export function SharedTable({ servers, onRevoke, onRemove }: SharedTableProps) {
  const t = useT(adminConnections);
  return (
    <>
      <div className="hidden overflow-hidden rounded-card border border-line bg-panel/85 md:block">
        <table className="w-full table-fixed border-collapse text-left">
          <caption className="sr-only">{t("sharedCaption")}</caption>
          <colgroup>
            <col className="w-[18%]" />
            <col className="w-[26%]" />
            <col className="w-[16%]" />
            <col className="w-[12%]" />
            <col className="w-[12%]" />
            <col className="w-[16%]" />
          </colgroup>
          <thead className="border-b border-line-strong">
            <tr>
              <th scope="col" className={HEAD}>{t("colServer")}</th>
              <th scope="col" className={HEAD}>{t("colSharedKey")}</th>
              <th scope="col" className={HEAD}>{t("colState")}</th>
              <th scope="col" className={HEAD}>{t("colLastUsed")}</th>
              <th scope="col" className={HEAD}>{t("colExpires")}</th>
              <th scope="col" className={HEAD}>
                <span className="sr-only">{t("colActions")}</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {servers.map((s) => (
              <tr key={s.slug} className="border-t border-line first:border-t-0 hover:bg-white/3">
                <td className="px-3 py-3"><ServerCell slug={s.slug} /></td>
                <td className="px-3 py-3">
                  <span className="block text-[13px] break-words text-fg">{sharedSummary(s, t)}</span>
                  <span className="block text-xs text-fg-muted">{usingText(s.using_shared, t)}</span>
                </td>
                <td className="px-3 py-3"><State server={s} /></td>
                <td className="px-3 py-3 text-xs text-fg-muted">{lastUsedText(s.shared?.last_used_at ?? null)}</td>
                <td className="px-3 py-3">{s.shared ? <ExpiryText iso={s.shared.expires_at} authKind={s.auth_kind} /> : null}</td>
                <td className="px-3 py-3 text-right"><Actions server={s} onRevoke={onRevoke} onRemove={onRemove} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <ul className="flex flex-col gap-2.5 md:hidden">
        {servers.map((s) => (
          <li key={s.slug} className="rounded-card border border-line bg-panel/85 p-3.5">
            <div className="flex items-center justify-between gap-3">
              <ServerCell slug={s.slug} />
              <KeyBadge kind="shared" />
            </div>
            <p className="mt-3 text-[13px] break-words text-fg">{sharedSummary(s, t)}</p>
            <p className="text-xs text-fg-muted">{usingText(s.using_shared, t)}</p>
            <div className="mt-3 border-t border-line pt-3"><State server={s} /></div>
            <div className="mt-2 flex justify-end"><Actions server={s} onRevoke={onRevoke} onRemove={onRemove} /></div>
          </li>
        ))}
      </ul>
    </>
  );
}
