"use client";

import { LoadError, RowsSkeleton } from "../../../components/admin-servers/load-states";
import { formatCommit, formatUptime } from "../../../components/admin-servers/probe-format";
import { AppShell } from "../../../components/shell/AppShell";
import { PageHeader } from "../../../components/shell/PageHeader";
import { useLoad } from "../../../lib/admin-types";
import { formatDateTime } from "../../../lib/format";
import { useT, type Translator } from "../../../lib/i18n";
import { adminSettings } from "../../../lib/i18n/messages/adminSettings";
import type { SystemInfo } from "../../../lib/stats-types";

function factsOf(info: SystemInfo, t: Translator<typeof adminSettings.en>): [string, string][] {
  const commit = formatCommit(info);
  return [
    [t("version"), info.version],
    ...(commit ? ([[t("commit"), commit]] as [string, string][]) : []),
    [t("go"), info.go_version],
    [t("publicUrl"), info.public_url],
    [t("startedAt"), `${formatDateTime(info.started_at)} (${formatUptime(info.started_at)})`],
    [t("theme"), t("themeValue")],
    [t("area"), t("areaValue")],
  ];
}

/** Read-only facts about this instance; there are no writes in this phase. */
export default function AdminSettingsPage() {
  const t = useT(adminSettings);
  const system = useLoad<SystemInfo>("/system");
  return (
    <AppShell surface="admin">
      <title>{t("pageTitle")}</title>
      <div className="flex flex-col gap-6">
        <PageHeader title={t("title")} description={t("description")} />
        {system.error ? <LoadError error={system.error} onRetry={system.reload} /> : null}
        {!system.error && !system.data ? <RowsSkeleton rows={3} label={t("loading")} /> : null}
        {system.data ? (
          <dl className="grid max-w-2xl grid-cols-[minmax(8rem,auto)_minmax(0,1fr)] gap-x-6 gap-y-3 rounded-card border border-line bg-panel/85 p-5 text-sm">
            {factsOf(system.data, t).map(([term, value]) => (
              <div key={term} className="col-span-full grid grid-cols-subgrid items-baseline">
                <dt className="text-fg-muted">{term}</dt>
                <dd className="min-w-0 font-mono break-all text-fg">{value}</dd>
              </div>
            ))}
          </dl>
        ) : null}
      </div>
    </AppShell>
  );
}
