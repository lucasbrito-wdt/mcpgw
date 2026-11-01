"use client";

import { useFormat, useT } from "../../lib/i18n";
import { charts } from "../../lib/i18n/messages/charts";
import type { DenyStat, ToolStat } from "../../lib/stats-types";
import { formatLatency } from "../admin-logs/logs-format";

function ListCard({ title, empty, children }: { title: string; empty: string; children: React.ReactNode[] }) {
  return (
    <section aria-label={title} className="rounded-card border border-line bg-panel/70 p-4">
      <h2 className="text-sm font-semibold text-fg">{title}</h2>
      {children.length === 0 ? <p className="mt-3 text-sm text-fg-muted">{empty}</p> : <ol className="mt-3 flex flex-col divide-y divide-line">{children}</ol>}
    </section>
  );
}

/** Busiest tools of the window. Names and reasons are untrusted: rendered as text nodes only. */
export function TopTools({ tools }: { tools: readonly ToolStat[] }) {
  const t = useT(charts);
  const f = useFormat();
  return (
    <ListCard title={t("topToolsTitle")} empty={t("emptyPeriod")}>
      {tools.map((tool) => (
        <li key={`${tool.server}/${tool.tool}`} className="flex items-baseline justify-between gap-3 py-2 text-sm">
          <span className="min-w-0 truncate font-mono text-fg">
            {tool.server}.{tool.tool}
          </span>
          <span className="flex shrink-0 items-baseline gap-3 text-xs text-fg-muted tabular-nums">
            {tool.denied > 0 ? <span className="text-cut">{t("deniedCount", { n: f.number(tool.denied) })}</span> : null}
            {tool.p95_ms !== null ? <span>{t("p95", { value: formatLatency(tool.p95_ms) })}</span> : null}
            <span className="font-mono text-sm text-fg">{f.number(tool.calls)}</span>
          </span>
        </li>
      ))}
    </ListCard>
  );
}

/** Most frequent refusals by server, tool and reason. */
export function TopDenies({ denies }: { denies: readonly DenyStat[] }) {
  const t = useT(charts);
  const f = useFormat();
  return (
    <ListCard title={t("topDeniesTitle")} empty={t("noDenies")}>
      {denies.map((d) => (
        <li key={`${d.server}/${d.tool}/${d.reason}`} className="flex items-baseline justify-between gap-3 py-2 text-sm">
          <span className="min-w-0">
            <span className="block truncate font-mono text-fg">
              {d.server}.{d.tool}
            </span>
            <span className="block truncate text-xs text-fg-muted">{d.reason}</span>
          </span>
          <span className="shrink-0 font-mono text-sm text-cut tabular-nums">{f.number(d.count)}</span>
        </li>
      ))}
    </ListCard>
  );
}
