"use client";

import { useRef, useState } from "react";

import { api, ApiError } from "../../lib/api";
import { initialsOf } from "../../lib/format";
import { useT } from "../../lib/i18n";
import { adminAccess } from "../../lib/i18n/messages/adminAccess";
import type { AccessLevel } from "../../lib/types";
import { ServiceLogo } from "../brand/ServiceLogo";
import { useToast } from "../ui/toast";
import { cn } from "../../lib/utils";

import { CeilingGauge, CeilingSkyline } from "./CeilingGauge";

/** Ceiling per `${userId}|${slug}`; a missing key means "Sem acesso". */
export type CeilingState = Record<string, AccessLevel>;

export interface CeilingUser {
  id: string;
  email: string;
}

export const ceilingKey = (userId: string, slug: string): string => `${userId}|${slug}`;

const LEVELS: readonly AccessLevel[] = ["none", "read", "write", "destructive"];

/** Narrows an API level string; anything unknown reads as no access. */
export const parseLevel = (level: string): AccessLevel => LEVELS.find((l) => l === level) ?? "none";

/** Pure reducer: returns a new state with the cell set; `none` removes it. The input is never mutated. */
export function applyCeilingChange(state: CeilingState, userId: string, slug: string, level: AccessLevel): CeilingState {
  const next = { ...state };
  if (level === "none") delete next[ceilingKey(userId, slug)];
  else next[ceilingKey(userId, slug)] = level;
  return next;
}

export interface CeilingMatrixProps {
  users: readonly CeilingUser[];
  servers: readonly string[];
  /** Ceilings loaded from `GET /ceilings`; the matrix owns its copy afterwards. */
  initial: CeilingState;
}

/** Accounts on the left, each with its access silhouette; the selected one shows a ceiling gauge per server. A change shows at once and rolls back if `PUT /ceilings/{user_id}/{slug}` fails. */
export function CeilingMatrix({ users, servers, initial }: CeilingMatrixProps) {
  const t = useT(adminAccess);
  const toast = useToast();
  const [state, setState] = useState<CeilingState>(initial);
  const [pending, setPending] = useState<ReadonlySet<string>>(new Set());
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);

  const setBusy = (key: string, busy: boolean) =>
    setPending((current) => {
      const next = new Set(current);
      if (busy) next.add(key);
      else next.delete(key);
      return next;
    });

  async function change(user: CeilingUser, slug: string, level: AccessLevel) {
    const key = ceilingKey(user.id, slug);
    const previous = state[key] ?? "none";
    setState((current) => applyCeilingChange(current, user.id, slug, level));
    setBusy(key, true);
    try {
      await api<void>("admin", `/ceilings/${encodeURIComponent(user.id)}/${encodeURIComponent(slug)}`, { method: "PUT", body: { level } });
    } catch (e) {
      setState((current) => applyCeilingChange(current, user.id, slug, previous));
      toast(e instanceof ApiError ? e.message : t("saveError"), { tone: "cut" });
    } finally {
      setBusy(key, false);
    }
  }

  const selected = users.find((u) => u.id === selectedId) ?? users[0];
  const levelsOf = (user: CeilingUser) => servers.map((slug) => state[ceilingKey(user.id, slug)] ?? "none");
  const grantedOf = (user: CeilingUser) => levelsOf(user).filter((level) => level !== "none").length;

  const select = (id: string) => {
    setSelectedId(id);
    // Stacked layout: the detail sits under the list, so move focus (and the view) to it.
    const wide = window.matchMedia("(min-width: 1024px)").matches;
    requestAnimationFrame(() => headingRef.current?.focus({ preventScroll: wide }));
  };

  return (
    <div className="grid gap-6 lg:grid-cols-[20rem_minmax(0,1fr)] lg:items-start">
      <nav aria-label={t("accountsLabel")} className="lg:sticky lg:top-6">
        <ul className="flex flex-col gap-1">
          {users.map((user) => {
            const active = user.id === selected?.id;
            const granted = grantedOf(user);
            return (
              <li key={user.id}>
                <button
                  type="button"
                  aria-current={active ? "true" : undefined}
                  onClick={() => select(user.id)}
                  className={cn(
                    "focus-ring flex w-full items-center gap-3 rounded-card border px-3 py-2.5 text-left transition-colors duration-150 ease-switch",
                    active ? "border-signal/50 bg-signal-wash" : "border-transparent hover:border-line hover:bg-white/4",
                  )}
                >
                  <span aria-hidden className="grid size-8 shrink-0 place-items-center rounded-full bg-guide text-[11px] font-semibold text-signal-fg">
                    {initialsOf(user.email)}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-[13px] font-medium text-fg">{user.email}</span>
                    <span className="block text-xs text-fg-muted">{t("accessCount", { n: granted, total: servers.length })}</span>
                  </span>
                  <CeilingSkyline levels={levelsOf(user)} />
                </button>
              </li>
            );
          })}
        </ul>
      </nav>

      {selected ? (
        <section aria-labelledby={`${selected.id}-ceilings`} className="rounded-card border border-line bg-panel/85">
          <header className="flex flex-wrap items-center gap-4 border-b border-line px-5 py-4">
            <span aria-hidden className="grid size-11 shrink-0 place-items-center rounded-full bg-guide text-sm font-semibold text-signal-fg">
              {initialsOf(selected.email)}
            </span>
            <div className="min-w-0 flex-1">
              <h2 ref={headingRef} tabIndex={-1} id={`${selected.id}-ceilings`} className="truncate text-base font-semibold text-fg outline-none">
                {selected.email}
              </h2>
              <p className="mt-0.5 text-xs text-fg-muted">{t("detailHint")}</p>
            </div>
            <span className="scale-150 origin-right pr-1">
              <CeilingSkyline levels={levelsOf(selected)} />
            </span>
          </header>
          <ul>
            {servers.map((slug) => {
              const key = ceilingKey(selected.id, slug);
              return (
                <li
                  key={slug}
                  className="flex flex-col gap-2 border-t border-line px-5 py-2.5 first:border-t-0 sm:flex-row sm:items-center sm:justify-between sm:gap-4"
                >
                  <span className="flex min-w-0 items-center gap-2.5">
                    <ServiceLogo slug={slug} name={slug} variant="catalog" size="xs" />
                    <span className="truncate font-mono text-[13px] font-medium text-fg">{slug}</span>
                  </span>
                  <CeilingGauge
                    label={t("ceilingLabel", { email: selected.email, slug })}
                    value={state[key] ?? "none"}
                    disabled={pending.has(key)}
                    onValueChange={(level) => void change(selected, slug, level)}
                  />
                </li>
              );
            })}
          </ul>
        </section>
      ) : null}
    </div>
  );
}
