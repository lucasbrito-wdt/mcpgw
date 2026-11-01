// Ported from design/v5/src/components/admin-review/review-screen.tsx on 2026-10-31.
// Hash states (#empty, #approve) became React state; the queue and the tool come from the API.
"use client";

import { CircleCheck, ShieldCheck } from "lucide-react";
import { useRef, useState } from "react";

import { useLoad, type Paged, type Tool, type ToolDetail } from "../../lib/admin-types";
import { api, ApiError } from "../../lib/api";
import { useT } from "../../lib/i18n";
import { adminReview } from "../../lib/i18n/messages/adminReview";
import type { AccessLevel } from "../../lib/types";
import { LoadError, RowsSkeleton } from "../admin-servers/load-states";
import { AppShell } from "../shell/AppShell";
import { PageHeader } from "../shell/PageHeader";
import { Button } from "../ui/button";
import { Dialog } from "../ui/dialog";
import { EmptyState } from "../ui/empty-state";
import { useToast } from "../ui/toast";

import { ReviewDecision } from "./review-decision";
import { ReviewDetailView } from "./review-detail";
import { ReviewQueue } from "./review-queue";
import { toolKey } from "./tool-key";

const toolPath = (tool: Pick<Tool, "server" | "name">) => `/tools/${encodeURIComponent(tool.server)}/${encodeURIComponent(tool.name)}`;

export function ReviewScreen() {
  const t = useT(adminReview);
  const queue = useLoad<Paged<Tool>>("/tools?status=pending_approval&limit=200");
  const toast = useToast();
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<string | null>(null);
  const [bulk, setBulk] = useState<string | null>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);

  const pending = queue.data?.items ?? [];
  const item = pending.find((tool) => toolKey(tool) === selectedKey) ?? pending[0] ?? null;
  const detail = useLoad<ToolDetail>(item ? toolPath(item) : null);
  const shown = detail.data;

  const select = (key: string) => {
    setSelectedKey(key);
    // On the stacked layout the detail is below the queue; bring it into view.
    const wide = window.matchMedia("(min-width: 1024px)").matches;
    requestAnimationFrame(() => headingRef.current?.focus({ preventScroll: wide }));
  };

  async function approve(tool: ToolDetail, level: AccessLevel) {
    setBusy(true);
    try {
      if (level !== "none" && level !== tool.level) await api<void>("admin", toolPath(tool), { method: "PATCH", body: { level } });
      await api("admin", `${toolPath(tool)}/approve`, { method: "POST", body: { hash: tool.hash } });
      setOutcome(t("outcomeApproved", { server: tool.server, name: tool.name, level: t(`levelText_${level}`) }));
      setSelectedKey(null);
      queue.reload();
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        toast(t("stale"), { tone: "warn" });
        detail.reload();
        queue.reload();
      } else {
        toast(e instanceof ApiError ? e.message : t("approveError"), { tone: "cut" });
      }
    } finally {
      setBusy(false);
    }
  }

  async function approveAll(slug: string) {
    setBusy(true);
    try {
      const result = await api<{ approved: string[] }>("admin", `/servers/${encodeURIComponent(slug)}/approve-all`, { method: "POST" });
      setOutcome(t("outcomeApprovedAll", { n: result.approved.length, slug }));
      setSelectedKey(null);
      setBulk(null);
      queue.reload();
    } catch (e) {
      toast(e instanceof ApiError ? e.message : t("approveAllError"), { tone: "cut" });
    } finally {
      setBusy(false);
    }
  }

  return (
    <AppShell surface="admin" reviewCount={queue.data ? pending.length : undefined}>
      <title>{t("pageTitle")}</title>
      <div className="flex flex-col gap-6">
        <PageHeader title={t("title")} description={t("description")} />

        <p aria-live="polite" className={outcome ? "flex items-start gap-2 rounded-card border border-line bg-panel/70 px-4 py-3 text-sm text-fg" : "sr-only"}>
          {outcome ? (
            <>
              <CircleCheck className="mt-0.5 size-4 shrink-0 text-ok" aria-hidden />
              {outcome}
            </>
          ) : null}
        </p>

        {queue.error ? <LoadError error={queue.error} onRetry={queue.reload} /> : null}
        {!queue.error && !queue.data ? <RowsSkeleton rows={4} label={t("loadingQueue")} /> : null}
        {queue.data && !item ? (
          <EmptyState icon={ShieldCheck} title={t("emptyTitle")}>
            {t("emptyBody")}
          </EmptyState>
        ) : null}

        {item ? (
          <div className="grid gap-6 lg:grid-cols-[18rem_minmax(0,1fr)] lg:items-start">
            <div className="flex flex-col gap-2 lg:sticky lg:top-8">
              <h2 className="flex items-baseline gap-2 px-1 text-sm font-semibold text-fg">
                {t("pending")} <span className="font-mono text-xs text-fg-muted">{pending.length}</span>
              </h2>
              <ReviewQueue items={pending} selected={toolKey(item)} onSelect={select} onApproveAll={setBulk} />
            </div>
            <div className="min-w-0">
              {detail.error ? <LoadError error={detail.error} onRetry={() => { detail.reload(); queue.reload(); }} /> : null}
              {!detail.error && !detail.data ? <RowsSkeleton rows={3} label={t("loadingTool")} /> : null}
              {shown ? (
                <ReviewDetailView key={`${toolKey(shown)}/${shown.hash}`} tool={shown} headingRef={headingRef}>
                  <ReviewDecision tool={shown} busy={busy} onApprove={(level) => void approve(shown, level)} />
                </ReviewDetailView>
              ) : null}
            </div>
          </div>
        ) : null}
      </div>

      <Dialog
        open={bulk !== null}
        onOpenChange={(next) => !next && !busy && setBulk(null)}
        title={t("bulkTitle", { slug: bulk ?? "" })}
        description={t("bulkDescription")}
        footer={
          <>
            <Button variant="ghost" disabled={busy} onClick={() => setBulk(null)}>
              {t("cancel")}
            </Button>
            <Button variant="primary" loading={busy} onClick={() => bulk && void approveAll(bulk)}>
              {t("approveAll")}
            </Button>
          </>
        }
      />
    </AppShell>
  );
}
