// Ported from design/v5/src/components/admin-connections/revoke-dialog.tsx on 2026-10-31.
// This is the shell shared by connections (reason required) and consents/grants (plain confirm).
"use client";

import { CircleAlert } from "lucide-react";
import type { ReactNode } from "react";

import { useT } from "../../lib/i18n";
import { adminRevoke } from "../../lib/i18n/messages/adminRevoke";
import { Button } from "../ui/button";
import { Dialog } from "../ui/dialog";

export const REASON_MAX = 500;

const TEXTAREA =
  "min-h-20 w-full resize-y rounded-control border border-fg-subtle bg-ink/70 px-3 py-2 text-sm text-fg outline-none transition-[border-color,box-shadow] duration-150 ease-switch placeholder:text-fg-muted/80 hover:border-fg-muted focus-visible:border-signal focus-visible:ring-3 focus-visible:ring-signal/30 aria-invalid:border-cut";

export interface RevokeDialogProps {
  open: boolean;
  title: string;
  description?: ReactNode;
  confirmLabel?: string;
  busy?: boolean;
  /** Message from the failed revoke; the dialog stays open so the person can retry. */
  error?: string;
  /** Pass a string to require a reason (1 to 500 characters); leave undefined for a plain confirm. */
  reason?: string;
  onReasonChange?: (reason: string) => void;
  /** Content above the reason field (preset reasons, what changes for callers). */
  children?: ReactNode;
  onConfirm: () => void;
  onClose: () => void;
}

/** Revoke confirmation. With `reason` set, the confirm button stays off until the text is 1 to 500 characters. */
export function RevokeDialog({ open, title, description, confirmLabel, busy = false, error, reason, onReasonChange, children, onConfirm, onClose }: RevokeDialogProps) {
  const t = useT(adminRevoke);
  const needsReason = reason !== undefined;
  const trimmed = (reason ?? "").trim();
  const ready = !needsReason || (trimmed.length > 0 && trimmed.length <= REASON_MAX);

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !busy) onClose();
      }}
      title={title}
      description={description}
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            {t("cancel")}
          </Button>
          <Button variant="danger" onClick={onConfirm} disabled={!ready} loading={busy} aria-describedby={needsReason ? "revoke-hint" : undefined}>
            {error ? t("retry") : (confirmLabel ?? t("revoke"))}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        {children}
        {needsReason ? (
          <div className="flex flex-col gap-1.5">
            <label htmlFor="revoke-reason-text" className="text-[13px] font-semibold text-fg">
              {t("reasonLabel")}
            </label>
            <textarea
              id="revoke-reason-text"
              value={reason}
              onChange={(e) => onReasonChange?.(e.target.value)}
              maxLength={REASON_MAX}
              disabled={busy}
              className={TEXTAREA}
              placeholder={t("reasonPlaceholder")}
              aria-invalid={trimmed === "" ? true : undefined}
            />
            <p id="revoke-hint" className="text-xs leading-5 text-fg-muted">
              {ready ? t("hintReady") : t("hintMissing")}
            </p>
          </div>
        ) : null}
        {error ? (
          <p role="alert" className="flex items-start gap-2 rounded-card border border-cut/40 bg-cut-wash p-3 text-xs leading-5 text-fg">
            <CircleAlert className="mt-0.5 size-4 shrink-0 text-cut" aria-hidden />
            <span>{error}</span>
          </p>
        ) : null}
      </div>
    </Dialog>
  );
}
