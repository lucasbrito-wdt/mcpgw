// Ported from design/v5/src/components/admin-review/review-decision.tsx on 2026-10-31.
// "Manter bloqueada" with a reason is omitted: there is no reject endpoint, a tool just stays pending (plan gap G4).
"use client";

import { useState } from "react";

import type { ToolDetail } from "../../lib/admin-types";
import { levelLabel } from "../../lib/format";
import { useT } from "../../lib/i18n";
import { adminReview } from "../../lib/i18n/messages/adminReview";
import type { AccessLevel } from "../../lib/types";
import { LevelSelect } from "../admin/LevelSelect";
import { Button } from "../ui/button";
import { Dialog } from "../ui/dialog";

export interface ReviewDecisionProps {
  tool: ToolDetail;
  busy: boolean;
  /** Sends the approval for `tool.hash`; `level` is the level chosen for release. */
  onApprove: (level: AccessLevel) => void;
}

const isSettable = (level: string): level is Exclude<AccessLevel, "none"> => level === "read" || level === "write" || level === "destructive";

/** Level at release and the approval, confirmed in a dialog that shows the hash being approved. */
export function ReviewDecision({ tool, busy, onApprove }: ReviewDecisionProps) {
  const t = useT(adminReview);
  const [level, setLevel] = useState<AccessLevel>(isSettable(tool.level) ? tool.level : "read");
  const [confirming, setConfirming] = useState(false);

  return (
    <section aria-label={t("decisionAria")} className="flex flex-col gap-4 rounded-card border border-line bg-panel/70 p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 className="text-[13px] font-semibold text-fg">{t("levelAtRelease")}</h3>
          <p className="text-xs text-fg-muted">{t("currentLevel", { level: isSettable(tool.level) ? levelLabel(tool.level) : tool.level })}</p>
        </div>
        <LevelSelect label={t("levelAtRelease")} value={level} onValueChange={setLevel} />
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <Button variant="primary" loading={busy} onClick={() => setConfirming(true)}>
          {t("approve")}
        </Button>
        <p className="text-xs text-fg-muted">{t("approveNote")}</p>
      </div>

      <Dialog
        open={confirming}
        onOpenChange={(next) => !busy && setConfirming(next)}
        title={t("confirmTitle", { name: tool.name })}
        description={t("confirmDescription")}
        footer={
          <>
            <Button variant="ghost" disabled={busy} onClick={() => setConfirming(false)}>
              {t("cancel")}
            </Button>
            <Button
              variant="primary"
              loading={busy}
              onClick={() => {
                onApprove(level);
                setConfirming(false);
              }}
            >
              {t("confirmApprove")}
            </Button>
          </>
        }
      >
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-[13px]">
          <dt className="text-fg-muted">{t("hash")}</dt>
          <dd className="font-mono break-all text-fg">{tool.hash}</dd>
          <dt className="text-fg-muted">{t("level")}</dt>
          <dd className="text-fg">{levelLabel(level)}</dd>
        </dl>
      </Dialog>
    </section>
  );
}
