// Ported from design/v5/src/app/portal/_components/attention-strip.tsx on 2026-10-31.
"use client";

import { TriangleAlert } from "lucide-react";
import Link from "next/link";

import { connectHref } from "../catalog/server-card";
import { ServiceLogo } from "../brand/ServiceLogo";
import { RouteLine } from "../signature/RouteLine";
import { Pill } from "../ui/badge";
import { buttonVariants } from "../ui/button-variants";
import { countLabel } from "../../hooks/countLabel";
import { useT } from "../../lib/i18n";
import { portalServers } from "../../lib/i18n/messages/portalServers";
import { appName, connectionStatusLabel, problemOf, problemReason, type PortalApp } from "../../lib/portal-types";
import type { ConnectionProblem, RouteCut } from "../../lib/types";

const CUT_OF: Record<ConnectionProblem, RouteCut> = { revoked: "provider", expired: "key", scopes: "scopes" };

/**
 * The viewer's broken connections, listed once (never repeated as cards below) with the reason and
 * the one action that fixes each. Filtered by the same search as the grid.
 */
export function AttentionStrip({ apps }: { apps: readonly PortalApp[] }) {
  const t = useT(portalServers);
  if (apps.length === 0) return null;
  return (
    <section aria-labelledby="attention-title" className="rounded-card border border-line bg-panel/90">
      <header className="flex flex-wrap items-start gap-x-3 gap-y-1 border-b border-line px-4 py-3.5 sm:px-5">
        <TriangleAlert className="mt-0.5 size-4 shrink-0 text-warn" aria-hidden />
        <div className="min-w-0 flex-1">
          <h2 id="attention-title" className="text-sm font-semibold text-fg">
            {countLabel(t, "failing", apps.length)}
          </h2>
          <p className="mt-0.5 text-[13px] text-fg-muted">{t("failingBody")}</p>
        </div>
      </header>
      <ul className="divide-y divide-line">
        {apps.map((app) => {
          const name = appName(app);
          const problem = problemOf(app.status);
          if (!problem) return null;
          return (
            <li key={app.slug} className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-3 gap-y-3 px-4 py-3.5 sm:px-5 md:grid-cols-[auto_minmax(0,1fr)_auto_auto] md:gap-x-5">
              <ServiceLogo slug={app.slug} name={name} variant="catalog" size="md" />
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                  <Link href={connectHref(app.slug)} className="focus-ring rounded-[4px] text-sm font-semibold text-fg hover:underline">
                    {name}
                  </Link>
                  <Pill kind={problem} />
                </div>
                <p className="mt-1 text-[13px] leading-5 text-pretty text-fg-muted">{problemReason(app.status, name)}</p>
              </div>
              <RouteLine
                size="mini"
                keyKind="personal"
                result="error"
                cut={CUT_OF[problem]}
                label={t("routeInterrupted", { name, status: connectionStatusLabel(app.status).toLowerCase() })}
                className="hidden md:block"
              />
              <Link
                href={connectHref(app.slug)}
                aria-label={t("reconnectAria", { name })}
                className={buttonVariants({ variant: "secondary", size: "sm", className: "col-span-2 justify-self-start md:col-span-1" })}
              >
                {t("reconnect")}
              </Link>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
