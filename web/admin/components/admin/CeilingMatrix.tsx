"use client";

import { useState } from "react";

import { api, ApiError } from "../../lib/api";
import { useT } from "../../lib/i18n";
import { adminAccess } from "../../lib/i18n/messages/adminAccess";
import type { AccessLevel } from "../../lib/types";
import { ServiceLogo } from "../brand/ServiceLogo";
import { useToast } from "../ui/toast";

import { LevelSelect } from "./LevelSelect";

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

const HEAD = "px-3 py-2 text-xs font-medium text-fg-muted";

/** Users (rows) by servers (columns). A change shows at once and rolls back if `PUT /ceilings/{user_id}/{slug}` fails. */
export function CeilingMatrix({ users, servers, initial }: CeilingMatrixProps) {
  const t = useT(adminAccess);
  const toast = useToast();
  const [state, setState] = useState<CeilingState>(initial);
  const [pending, setPending] = useState<ReadonlySet<string>>(new Set());

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

  return (
    <div className="overflow-x-auto rounded-card border border-line bg-panel/85">
      <table className="w-max min-w-full border-collapse text-left">
        <caption className="sr-only">{t("matrixCaption")}</caption>
        <thead className="border-b border-line-strong">
          <tr>
            <th scope="col" className={`${HEAD} sticky left-0 z-10 min-w-56 bg-panel`}>
              {t("person")}
            </th>
            {servers.map((slug) => (
              <th key={slug} scope="col" className={HEAD}>
                <span className="flex items-center gap-2">
                  <ServiceLogo slug={slug} name={slug} variant="catalog" size="xs" />
                  <span className="font-mono text-[13px] font-semibold text-fg">{slug}</span>
                </span>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {users.map((user) => (
            <tr key={user.id} className="border-t border-line first:border-t-0 hover:bg-white/3">
              <th scope="row" className="sticky left-0 z-10 min-w-56 truncate bg-panel px-3 py-2.5 text-left text-[13px] font-semibold text-fg">
                {user.email}
              </th>
              {servers.map((slug) => {
                const key = ceilingKey(user.id, slug);
                return (
                  <td key={slug} className="px-3 py-2.5">
                    <LevelSelect
                      allowNone
                      label={t("ceilingLabel", { email: user.email, slug })}
                      value={state[key] ?? "none"}
                      disabled={pending.has(key)}
                      onValueChange={(level) => void change(user, slug, level)}
                    />
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
