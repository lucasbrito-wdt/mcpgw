// Inline SVG, attributes only (CSP forbids style). Colors come from Tailwind tokens through currentColor.
"use client";

import { useFormat, useT } from "../../lib/i18n";
import { charts } from "../../lib/i18n/messages/charts";
import type { Stats } from "../../lib/stats-types";

import { buildTrafficPaths, peakBucket } from "./traffic-path";

const SIZE = { width: 640, height: 160 };
const CLOCK: Intl.DateTimeFormatOptions = { timeZone: "America/Sao_Paulo", hour: "2-digit", minute: "2-digit", hourCycle: "h23" };

/** Calls (area + line) and denied calls (line) per bucket of a stats window. */
export function TrafficChart({ stats }: { stats: Pick<Stats, "series" | "from" | "to" | "totals"> }) {
  const t = useT(charts);
  const f = useFormat();
  const formatClock = (iso: string) => f.date(iso, CLOCK);
  const geometry = buildTrafficPaths(stats.series, SIZE);
  const peak = peakBucket(stats.series);
  const peakCalls = peak >= 0 ? stats.series[peak].calls : 0;
  const summary =
    t("summary", { from: formatClock(stats.from), to: formatClock(stats.to), calls: f.number(stats.totals.calls), denied: f.number(stats.totals.denied) }) +
    (peak >= 0 ? ` ${t("peak", { n: f.number(peakCalls), time: formatClock(stats.series[peak].ts) })}` : "");

  return (
    <figure className="flex flex-col gap-2">
      <div className="flex gap-3">
        <span aria-hidden className="flex flex-col justify-between py-0.5 text-right font-mono text-[11px] text-fg-muted tabular-nums">
          <span>{f.number(geometry.max)}</span>
          <span>0</span>
        </span>
        <svg
          role="img"
          aria-label={summary}
          viewBox={`0 0 ${SIZE.width} ${SIZE.height}`}
          preserveAspectRatio="none"
          className="h-40 min-w-0 flex-1 overflow-visible"
        >
          <title>{summary}</title>
          {[0, 0.5, 1].map((fraction) => (
            <line
              key={fraction}
              x1={0}
              x2={SIZE.width}
              y1={SIZE.height * fraction}
              y2={SIZE.height * fraction}
              className="stroke-line"
              strokeWidth={1}
              vectorEffect="non-scaling-stroke"
            />
          ))}
          <path d={geometry.callsArea} className="fill-signal/15" />
          <path d={geometry.calls} fill="none" className="stroke-signal-fg" strokeWidth={2} strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
          <path d={geometry.denied} fill="none" className="stroke-cut" strokeWidth={2} strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
        </svg>
      </div>
      <figcaption className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 pl-9 text-xs text-fg-muted">
        <span aria-hidden className="font-mono tabular-nums">
          {formatClock(stats.from)}
        </span>
        <ul aria-hidden className="flex items-center gap-4">
          <li className="flex items-center gap-1.5">
            <span className="h-0.5 w-3 rounded-full bg-signal-fg" />
            {t("legendCalls")}
          </li>
          <li className="flex items-center gap-1.5">
            <span className="h-0.5 w-3 rounded-full bg-cut" />
            {t("legendDenied")}
          </li>
        </ul>
        <span aria-hidden className="font-mono tabular-nums">
          {formatClock(stats.to)}
        </span>
      </figcaption>
    </figure>
  );
}
