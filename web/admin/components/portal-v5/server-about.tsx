// Ported from design/v5/src/components/portal/server-about.tsx on 2026-10-31.
// Only contract facts: host, origin, instance counts and last call are not in the portal API.
"use client";

import { ExternalLink } from "lucide-react";
import Link from "next/link";
import type { ReactNode } from "react";

import { authLabelOf } from "../catalog/server-card";
import { countLabel } from "../../hooks/countLabel";
import { useT, type Translator } from "../../lib/i18n";
import { portalDetail } from "../../lib/i18n/messages/portalDetail";
import { categoryLabel, type PortalAppDetail } from "../../lib/portal-types";

function levelSummary(tools: PortalAppDetail["tools"], t: Translator<typeof portalDetail.en>): string | null {
  if (tools.length === 0) return null;
  const count = (level: string) => tools.filter((tool) => tool.level === level).length;
  return countLabel(t, "levelSummary", count("destructive"), { read: count("read"), write: count("write") });
}

function Row({ term, children }: { term: string; children: ReactNode }) {
  return (
    <div className="grid gap-0.5 py-2.5 first:pt-0 last:pb-0">
      <dt className="text-xs text-fg-muted">{term}</dt>
      <dd className="text-[13px] leading-5 text-pretty text-fg">{children}</dd>
    </div>
  );
}

/** "About this server": the facts a person checks before trusting a server with a key. */
export function ServerAbout({ app, isAdmin }: { app: PortalAppDetail; isAdmin: boolean }) {
  const t = useT(portalDetail);
  const levels = levelSummary(app.tools, t);
  return (
    <aside aria-labelledby="about-title" className="flex flex-col gap-4 rounded-card border border-line bg-panel/90 p-4 lg:sticky lg:top-8">
      <h2 id="about-title" className="text-sm font-semibold text-fg">
        {t("aboutTitle")}
      </h2>
      <dl className="divide-y divide-line">
        <Row term={t("howItConnects")}>{app.credential_env ? t("viaEnv", { auth: authLabelOf(app), env: app.credential_env }) : authLabelOf(app)}</Row>
        {app.category ? <Row term={t("category")}>{categoryLabel(app.category)}</Row> : null}
        {levels ? <Row term={t("toolsByLevel", { n: app.tools.length })}>{levels}</Row> : null}
        {app.auth_kind === "none" ? null : <Row term={t("sharedConnection")}>{t(app.shared_available ? "sharedYes" : "sharedNo")}</Row>}
      </dl>
      {isAdmin ? (
        <Link href="/admin/servers/" className="focus-ring inline-flex items-center gap-1.5 self-start rounded-[4px] text-xs font-medium text-signal-fg hover:underline">
          {t("openInAdmin")}
          <ExternalLink className="size-3" aria-hidden />
        </Link>
      ) : null}
    </aside>
  );
}
