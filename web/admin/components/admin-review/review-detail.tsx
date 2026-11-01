// Ported from design/v5/src/components/admin-review/review-detail.tsx on 2026-10-31.
// "Blocked calls" (route line, count, link to the log) is omitted: the contract has no per-tool denied-call count (plan gap G4).
"use client";

import type { ReactNode, Ref } from "react";

import type { ToolDetail } from "../../lib/admin-types";
import { useT } from "../../lib/i18n";
import { adminReview } from "../../lib/i18n/messages/adminReview";
import { ToolDiff } from "../admin/ToolDiff";
import { ServiceLogo } from "../brand/ServiceLogo";
import { Badge } from "../ui/badge";

export interface ReviewDetailViewProps {
  tool: ToolDetail;
  headingRef: Ref<HTMLHeadingElement>;
  children: ReactNode;
}

/** One pending tool: its hashes and definition. The decision goes in children. */
export function ReviewDetailView({ tool, headingRef, children }: ReviewDetailViewProps) {
  const t = useT(adminReview);
  const isNew = !tool.approved_hash;
  return (
    <article aria-labelledby="review-tool" className="flex min-w-0 flex-col gap-5">
      <header className="flex items-start gap-3">
        <ServiceLogo slug={tool.server} name={tool.server} size="md" />
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <h2 id="review-tool" ref={headingRef} tabIndex={-1} className="font-mono text-lg font-medium break-all text-fg outline-none">
              {tool.name}
            </h2>
            {isNew ? <Badge tone="signal">{t("badgeNew")}</Badge> : <Badge tone="cut">{t("badgeBlocked")}</Badge>}
          </div>
          <p className="mt-1 text-sm text-pretty text-fg-muted">
            <span className="font-mono">{tool.server}</span>.{" "}
            {t(isNew ? "introNew" : "introChanged")}
          </p>
        </div>
      </header>
      <ToolDiff tool={tool} />
      {children}
    </article>
  );
}
