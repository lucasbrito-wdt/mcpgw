"use client";

import { Plus, Unplug } from "lucide-react";

import { Badge, type BadgeTone } from "../ui/badge";
import { Button } from "../ui/button";
import { formatDateTime } from "../../lib/format";
import { useT } from "../../lib/i18n";
import { portalCommon } from "../../lib/i18n/messages/portalCommon";
import { portalDetail } from "../../lib/i18n/messages/portalDetail";
import { connectionStatusLabel, type AuthMethod, type ConnectionStatus, type PortalProfile } from "../../lib/portal-types";

const METHOD_LABEL: Record<AuthMethod, "viaOauth" | "viaToken"> = { oauth: "viaOauth", token: "viaToken" };

const TONE: Partial<Record<ConnectionStatus, BadgeTone>> = { connected: "ok", expiring: "warn", expired: "cut", refresh_failed: "cut", revoked_by_admin: "cut", scopes_changed: "warn" };

export interface ProfileListProps {
  name: string;
  profiles: readonly PortalProfile[];
  onAdd: () => void;
  onDisconnect: (profile: string) => void;
}

/** "Your connections": one row per named connection of the viewer, with connect and disconnect per name. */
export function ProfileList({ name, profiles, onAdd, onDisconnect }: ProfileListProps) {
  const t = useT(portalDetail);
  const tCommon = useT(portalCommon);
  return (
    <section aria-labelledby="profiles-title" className="flex flex-col gap-4 rounded-card border border-line bg-panel/90 p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 id="profiles-title" className="text-sm font-semibold text-fg">
            {t("profilesTitle", { name })}
          </h2>
          <p className="mt-1 text-xs text-fg-muted">{t("profilesHint")}</p>
        </div>
        <Button variant="secondary" onClick={onAdd}>
          <Plus aria-hidden />
          {t("addConnection")}
        </Button>
      </div>
      {profiles.length === 0 ? (
        <p className="text-xs text-fg-muted">{t("noProfiles")}</p>
      ) : null}
      <ul className="divide-y divide-line">
        {profiles.map((item) => (
          <li key={item.profile} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 py-3 first:pt-0 last:pb-0">
            <div className="min-w-0">
              <p className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-[13px] font-semibold text-fg">{item.profile}</span>
                <Badge tone={TONE[item.status] ?? "neutral"}>{connectionStatusLabel(item.status)}</Badge>
              </p>
              <p className="mt-1 text-xs text-fg-muted">
                {item.method ? `${t(METHOD_LABEL[item.method])} · ` : ""}
                {item.url_host ? <><span className="font-mono text-fg">{item.url_host}</span> · </> : null}
                {item.account_label ? `${item.account_label} · ` : ""}
                {item.last4 ? `••••${item.last4} · ` : ""}
                {t("connectedOn", { date: formatDateTime(item.connected_at) })}
                {item.last_used_at ? ` · ${t("lastUsedOn", { date: formatDateTime(item.last_used_at) })}` : ""}
              </p>
              {item.env_set?.length ? (
                <p className="mt-1 text-xs text-fg-muted">
                  {t("ownVars")} <span className="font-mono text-fg">{item.env_set.join(", ")}</span>
                </p>
              ) : null}
            </div>
            <Button variant="ghost" size="sm" onClick={() => onDisconnect(item.profile)} aria-label={t("disconnectProfileAria", { profile: item.profile })} className="hover:text-cut">
              <Unplug aria-hidden />
              {tCommon("disconnect")}
            </Button>
          </li>
        ))}
      </ul>
    </section>
  );
}
