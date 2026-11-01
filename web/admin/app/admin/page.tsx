"use client";

import Link from "next/link";
import { useMemo } from "react";

import { LoadError, RowsSkeleton } from "../../components/admin-servers/load-states";
import { serverHref } from "../../components/admin-servers/server-cells";
import { AppShell } from "../../components/shell/AppShell";
import { PageHeader } from "../../components/shell/PageHeader";
import { TrafficPanel } from "../../components/charts/traffic-panel";
import { TopDenies, TopTools } from "../../components/charts/top-lists";
import { Switchboard } from "../../components/signature/Switchboard";
import { buttonVariants } from "../../components/ui/button-variants";
import { serverKeyKind, useLoad, type OAuthClientSummary, type Overview, type Paged, type ServerSummary } from "../../lib/admin-types";
import { useFormat, useT } from "../../lib/i18n";
import { adminOverview } from "../../lib/i18n/messages/adminOverview";
import type { Stats } from "../../lib/stats-types";
import type { Agent, SwitchboardServer } from "../../lib/types";
import { cn } from "../../lib/utils";

const MAX_AGENTS = 4;

interface TileProps {
  label: string;
  value: number;
  note?: string;
  href?: string;
  tone?: "warn" | "cut";
}

function StatTile({ label, value, note, href, tone }: TileProps) {
  const f = useFormat();
  const body = (
    <>
      <dt className="text-xs text-fg-muted">{label}</dt>
      <dd className="mt-1 flex items-baseline gap-2">
        <span className={cn("font-mono text-2xl font-medium tabular-nums", tone === "warn" && value > 0 ? "text-warn" : tone === "cut" && value > 0 ? "text-cut" : "text-fg")}>
          {f.number(value)}
        </span>
        {note ? <span className="text-xs text-fg-muted">{note}</span> : null}
      </dd>
    </>
  );
  const box = "block rounded-card border border-line bg-panel/70 px-4 py-3";
  return href ? (
    <Link href={href} className={cn(box, "focus-ring transition-colors duration-150 ease-switch hover:border-line-strong hover:bg-panel")}>
      {body}
    </Link>
  ) : (
    <div className={box}>{body}</div>
  );
}

function Stats({ overview }: { overview: Overview }) {
  const t = useT(adminOverview);
  return (
    <dl className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
      <StatTile label={t("tileServers")} value={overview.servers} note={overview.servers_disabled ? t("tileServersDisabled", { n: overview.servers_disabled }) : undefined} href="/admin/servers/" />
      <StatTile label={t("tileTools")} value={overview.tools_active} />
      <StatTile label={t("tileQuarantined")} value={overview.tools_quarantined} tone="warn" href="/admin/review/" />
      <StatTile label={t("tilePending")} value={overview.pending_approvals} tone="warn" href="/admin/review/" />
      <StatTile label={t("tileDenies")} value={overview.denies_24h} tone="cut" />
      <StatTile label={t("tileExecutions")} value={overview.executions_24h} />
    </dl>
  );
}

export default function AdminOverviewPage() {
  const t = useT(adminOverview);
  const overview = useLoad<Overview>("/overview");
  const servers = useLoad<Paged<ServerSummary>>("/servers?limit=200");
  const clients = useLoad<Paged<OAuthClientSummary>>("/clients?limit=20");
  const stats = useLoad<Stats>("/stats?window=24h");

  const board = useMemo(() => {
    const tiles: SwitchboardServer[] = (servers.data?.items ?? []).map((s) => ({ slug: s.slug, name: s.slug, key: serverKeyKind(s) }));
    const agents: Agent[] = (clients.data?.items ?? [])
      .slice(0, MAX_AGENTS)
      .map((c) => ({ id: c.name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, ""), name: c.name }));
    return { tiles, agents };
  }, [servers.data, clients.data]);

  return (
    <AppShell surface="admin" reviewCount={overview.data?.pending_approvals}>
      <title>{t("pageTitle")}</title>
      <div className="flex flex-col gap-6">
        <PageHeader title={t("title")} description={t("description")} />

        {overview.error ? <LoadError error={overview.error} onRetry={overview.reload} /> : null}
        {!overview.error && !overview.data ? <RowsSkeleton rows={2} label={t("loading")} /> : null}
        {overview.data ? <Stats overview={overview.data} /> : null}

        <section aria-label={t("trafficAria")} className="flex flex-col gap-3">
          <h2 className="text-sm font-semibold text-fg">{t("trafficTitle")}</h2>
          <TrafficPanel stats={stats.data} error={stats.error} onRetry={stats.reload} />
          {stats.data && stats.data.totals.calls > 0 ? (
            <div className="grid gap-3 lg:grid-cols-2">
              <TopTools tools={stats.data.top_tools} />
              <TopDenies denies={stats.data.top_denies} />
            </div>
          ) : null}
        </section>

        {board.tiles.length > 0 && board.agents.length > 0 ? (
          <Switchboard
            title={t("boardTitle")}
            titleAs="h2"
            description={t("boardDescription")}
            actions={
              <Link href="/admin/servers/" className={buttonVariants({ variant: "secondary", size: "lg" })}>
                {t("boardAction")}
              </Link>
            }
            servers={board.tiles}
            agents={board.agents}
            hrefFor={serverHref}
            agentsCaption={t("boardAgents")}
            maxTiles={10}
          />
        ) : null}
      </div>
    </AppShell>
  );
}
