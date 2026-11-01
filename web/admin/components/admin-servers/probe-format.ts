import { getLocale, translate, type Vars } from "../../lib/i18n";
import { serversProbe } from "../../lib/i18n/messages/serversProbe";
import type { ProbeResult, Stats, SystemInfo } from "../../lib/stats-types";

const copy = (key: keyof typeof serversProbe.en, vars?: Vars): string => translate(getLocale(), serversProbe, key, vars);

export type ProbeTone = "ok" | "warn" | "cut";
export interface ProbeMessage {
  tone: ProbeTone;
  text: string;
}

/** "server": a saved server (auth is a warning). "wizard": before creation (auth is expected, configured next). */
export type ProbeContext = "server" | "wizard";

/** Turns a probe result into the inline message. `error` is a scrubbed string from the backend, shown as received. */
export function formatProbe(result: ProbeResult, context: ProbeContext): ProbeMessage {
  if (result.ok) {
    const n = result.tools ?? 0;
    return { tone: "ok", text: copy(n === 1 ? "ok_one" : "ok_other", { n, ms: result.latency_ms }) };
  }
  if (result.requires_auth) {
    return context === "wizard"
      ? { tone: "ok", text: copy("reachable") }
      : { tone: "warn", text: result.status === null && result.error ? result.error : copy("requiresAuth", { status: result.status ?? 401 }) };
  }
  return { tone: "cut", text: result.error ?? copy("failed") };
}

export interface ToolUsage {
  calls: number;
  denied: number;
}

/** Looks up 7d usage for a tool of `slug` by name; a tool absent from top_tools has zero. */
export function toolUsage(stats: Stats | undefined, slug: string): (tool: string) => ToolUsage {
  const byName = new Map<string, ToolUsage>();
  for (const t of stats?.top_tools ?? []) {
    if (t.server === slug) byName.set(t.tool, { calls: t.calls, denied: t.denied });
  }
  return (tool) => byName.get(tool) ?? { calls: 0, denied: 0 };
}

const SHORT_COMMIT = 7;

export function formatCommit(info: SystemInfo): string | null {
  if (!info.commit) return null;
  const short = info.commit.slice(0, SHORT_COMMIT);
  return info.modified ? copy("commitModified", { short }) : short;
}

/** Relative time since the process started, e.g. "3 h 20 min ago". */
export function formatUptime(startedAt: string, now: Date = new Date()): string {
  const minutes = Math.floor((now.getTime() - Date.parse(startedAt)) / 60_000);
  if (minutes < 1) return copy("uptimeLessThanMinute");
  if (minutes < 60) return copy("uptimeMinutes", { m: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return copy("uptimeHours", { h: hours, m: minutes % 60 });
  return copy("uptimeDays", { d: Math.floor(hours / 24), h: hours % 24 });
}
