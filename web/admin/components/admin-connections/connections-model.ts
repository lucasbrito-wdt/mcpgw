// Ported from design/v5/src/components/admin-connections/connections-model.ts on 2026-10-31.
import { getLocale, translate } from "../../lib/i18n";
import { adminConnections } from "../../lib/i18n/messages/adminConnections";
import type { RouteCut } from "../../lib/types";

import type { ConnectionServer, UserConnection } from "./types";

export type Tab = "personal" | "shared" | "problem";

export const TABS: readonly Tab[] = ["personal", "shared", "problem"];

// Copy helpers read the active locale at call time; the components that call them also call useT, so a switch re-renders them.
type Key = keyof typeof adminConnections.en;
const tr = (key: Key, vars?: Record<string, string | number>) => translate(getLocale(), adminConnections, key, vars);

export const tabLabel = (tab: Tab): string => tr(`tab_${tab}`);

export const MAX_BULK = 100;

/** Statuses that are not a personal key: the person is served by the shared key or has nothing. */
const NOT_PERSONAL = new Set(["shared", "not_connected"]);
const PROBLEM = new Set(["expired", "refresh_failed", "revoked_by_admin"]);

export const personalRows = (items: readonly UserConnection[]): UserConnection[] => items.filter((c) => !NOT_PERSONAL.has(c.status));

export const rowId = (c: Pick<UserConnection, "server" | "user_id" | "profile">): string => `${c.server}/${c.user_id}/${c.profile}`;

export const isProblemRow = (c: UserConnection): boolean => PROBLEM.has(c.status);
export const isProblemServer = (s: ConnectionServer): boolean => s.shared !== null && s.shared.status !== "active";

export function matchesQuery(c: UserConnection, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return [c.email, c.account_label ?? "", c.server].some((part) => part.toLowerCase().includes(q));
}

export interface RouteState {
  result: "ok" | "error";
  cut?: RouteCut;
  /** Short text next to the glyph, also its accessible name. */
  label: string;
  tone: "ok" | "warn" | "cut";
}

const STATE = {
  connected: { result: "ok", labelKey: "state_active", tone: "ok" },
  active: { result: "ok", labelKey: "state_active", tone: "ok" },
  expiring: { result: "ok", labelKey: "state_expiring", tone: "warn" },
  expired: { result: "error", cut: "key", labelKey: "state_expired", tone: "warn" },
  refresh_failed: { result: "error", cut: "provider", labelKey: "state_refresh_failed", tone: "warn" },
  revoked_by_admin: { result: "error", cut: "key", labelKey: "state_revoked_by_admin", tone: "cut" },
} as const satisfies Record<string, Omit<RouteState, "label"> & { labelKey: Key }>;

export function routeState(status: string): RouteState {
  if (!Object.hasOwn(STATE, status)) return { result: "ok", label: status, tone: "ok" };
  const { labelKey, ...rest } = STATE[status as keyof typeof STATE];
  return { ...rest, label: tr(labelKey) };
}

export interface Effect {
  tone: "info" | "warn";
  text: string;
}

/** A personal row to revoke, or the shared connection of a server. */
export type RevokeTarget = { kind: "personal"; server: string; userId: string; profile: string; email: string } | { kind: "shared"; server: string };

export const targetId = (t: RevokeTarget): string => (t.kind === "shared" ? `${t.server}/shared` : `${t.server}/${t.userId}/${t.profile}`);

/** What the next calls do once these keys are gone: a personal key falls back to an active shared key, with none the call fails. */
export function revokeEffects(targets: readonly RevokeTarget[], servers: readonly ConnectionServer[]): Effect[] {
  const sharedActive = (slug: string) => servers.find((s) => s.slug === slug)?.shared?.status === "active" && !targets.some((t) => t.kind === "shared" && t.server === slug);
  const personal = targets.filter((t): t is Extract<RevokeTarget, { kind: "personal" }> => t.kind === "personal");
  const fallback = personal.filter((t) => sharedActive(t.server));
  const failing = personal.filter((t) => !sharedActive(t.server));
  const effects: Effect[] = [];

  if (fallback.length === 1) {
    effects.push({ tone: "info", text: tr("effectFallbackSingle", { email: fallback[0].email, server: fallback[0].server }) });
  } else if (fallback.length > 1) {
    effects.push({ tone: "info", text: tr("effectFallbackMany", { n: fallback.length }) });
  }

  if (failing.length === 1) {
    effects.push({ tone: "warn", text: tr("effectFailingSingle", { server: failing[0].server, email: failing[0].email }) });
  } else if (failing.length > 1) {
    const names = failing.slice(0, 3).map((t) => tr("effectNameItem", { email: t.email, server: t.server }));
    const more = failing.length > 3 ? tr("effectMore", { n: failing.length - 3 }) : "";
    effects.push({ tone: "warn", text: tr("effectFailingMany", { names: names.join(", "), more }) });
  }

  for (const t of targets.filter((x) => x.kind === "shared")) {
    effects.push({ tone: "warn", text: tr("effectShared", { server: t.server }) });
  }
  return effects;
}
