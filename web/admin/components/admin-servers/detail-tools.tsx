"use client";

import { ShieldCheck } from "lucide-react";
import Link from "next/link";
import { useState } from "react";

import { toolStatusLabel, useLoad, type AccessLevelSetting, type Paged, type Tool } from "../../lib/admin-types";
import { api, ApiError } from "../../lib/api";
import { truncate } from "../../lib/format";
import { useT } from "../../lib/i18n";
import { servers } from "../../lib/i18n/messages/servers";
import type { Stats } from "../../lib/stats-types";
import type { AccessLevel } from "../../lib/types";
import { LevelSelect } from "../admin/LevelSelect";
import { Badge } from "../ui/badge";
import { EmptyState } from "../ui/empty-state";
import { useToast } from "../ui/toast";

import { LoadError, RowsSkeleton } from "./load-states";
import { toolUsage } from "./probe-format";

const MAX_NAME = 80;

function Usage({ calls, denied }: { calls: number; denied: number }) {
  const t = useT(servers);
  return (
    <p className="ml-auto flex flex-col text-right text-xs text-fg-muted tabular-nums">
      <span>
        {t("usage7d")} <span className="font-mono text-fg">{calls}</span>
      </span>
      <span className={denied > 0 ? "text-cut" : undefined}>
        {t("deniedCount")} <span className="font-mono">{denied}</span>
      </span>
    </p>
  );
}

/** Tools of one server with the level each one is released at. Tool names are untrusted text. */
export function DetailTools({ slug }: { slug: string }) {
  const t = useT(servers);
  const tools = useLoad<Paged<Tool>>(`/tools?server=${encodeURIComponent(slug)}&limit=200`);
  // Usage is a nicety: while it loads or fails, the column shows nothing instead of blocking the list.
  const stats = useLoad<Stats>(`/stats?window=7d&server=${encodeURIComponent(slug)}&top=200`);
  const usageOf = toolUsage(stats.data, slug);
  const toast = useToast();
  const [saving, setSaving] = useState<string | null>(null);

  async function setLevel(tool: Tool, level: AccessLevel) {
    if (level === "none" || level === tool.level) return;
    setSaving(tool.name);
    try {
      await api<void>("admin", `/tools/${encodeURIComponent(slug)}/${encodeURIComponent(tool.name)}`, { method: "PATCH", body: { level } });
      toast(t("levelUpdated"), { tone: "ok" });
      tools.reload();
    } catch (e) {
      toast(e instanceof ApiError ? e.message : t("levelFailed"), { tone: "cut" });
    } finally {
      setSaving(null);
    }
  }

  if (tools.error) return <LoadError error={tools.error} onRetry={tools.reload} />;
  if (!tools.data) return <RowsSkeleton label={t("toolsLoading")} />;
  if (tools.data.items.length === 0) {
    return (
      <EmptyState icon={ShieldCheck} title={t("toolsEmptyTitle")}>
        {t("toolsEmptyBody")}
      </EmptyState>
    );
  }

  return (
    <ul className="flex flex-col divide-y divide-line rounded-card border border-line bg-panel/70" aria-label={t("toolsListLabel", { slug })}>
      {tools.data.items.map((tool) => (
        <li key={tool.name} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 px-4 py-3">
          <div className="min-w-0">
            <p className="font-mono text-[13px] break-all text-fg">{truncate(tool.name, MAX_NAME)}</p>
            <p className="mt-1 flex flex-wrap items-center gap-2 text-xs text-fg-muted">
              <Badge tone={tool.status === "active" ? "ok" : "warn"}>{toolStatusLabel(tool.status)}</Badge>
              {tool.overridden ? <span>{t("levelOverridden")}</span> : null}
              {tool.status === "pending_approval" ? (
                <Link href="/admin/review/" className="focus-ring rounded-[3px] text-signal-fg underline underline-offset-4">
                  {t("review")}
                </Link>
              ) : null}
            </p>
          </div>
          {stats.data ? <Usage {...usageOf(tool.name)} /> : null}
          <LevelSelect
            label={t("levelOf", { name: truncate(tool.name, MAX_NAME) })}
            value={tool.level as AccessLevelSetting}
            disabled={saving === tool.name}
            onValueChange={(level) => void setLevel(tool, level)}
          />
        </li>
      ))}
    </ul>
  );
}
