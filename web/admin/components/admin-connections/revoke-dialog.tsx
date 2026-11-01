// Ported from design/v5/src/components/admin-connections/revoke-dialog.tsx on 2026-10-31.
// The mock provider 503 became the real API error; "Outro motivo" is the free text field itself.
"use client";

import { Info, TriangleAlert } from "lucide-react";
import { useState } from "react";

import { api, ApiError } from "../../lib/api";
import { useT, type Translator } from "../../lib/i18n";
import { adminConnections } from "../../lib/i18n/messages/adminConnections";
import { RevokeDialog as RevokeDialogShell } from "../admin/RevokeDialog";
import { ReauthCancelled, useReauth } from "../admin/ReauthDialog";
import { OptionCard } from "../admin-servers/option-card";

import { revokeEffects, targetId, type RevokeTarget } from "./connections-model";
import type { ConnectionServer } from "./types";

const PRESET_REASONS = ["preset_left", "preset_leaked", "preset_unneeded"] as const;

type T = Translator<typeof adminConnections.en>;

export interface ConnectionRevokeDialogProps {
  /** Connections being revoked; null keeps the dialog closed. */
  targets: readonly RevokeTarget[] | null;
  servers: readonly ConnectionServer[];
  onClose: () => void;
  /** Ids (see `targetId`) that were revoked, also when only part of a batch went through. */
  onRevoked: (ids: readonly string[]) => void;
}

const deletePath = (t: RevokeTarget) => (t.kind === "shared" ? `/connections/${encodeURIComponent(t.server)}/shared` : `/connections/${encodeURIComponent(t.server)}/${encodeURIComponent(t.userId)}`);

const describeTarget = (target: RevokeTarget, t: T) => {
  if (target.kind === "shared") return t("targetShared", { server: target.server });
  const { email, server, profile } = target;
  return profile === "default" ? t("rowDescribe", { email, server }) : t("rowDescribeProfile", { email, server, profile });
};

/** Revoke one or many connections, one `DELETE` each, with the reason the person will read. */
export function ConnectionRevokeDialog({ targets, servers, onClose, onRevoked }: ConnectionRevokeDialogProps) {
  const t = useT(adminConnections);
  const reauth = useReauth();
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const [seen, setSeen] = useState<typeof targets>(null);

  // Fresh form every time the dialog opens on a new selection.
  if (targets !== seen) {
    setSeen(targets);
    setReason("");
    setError(undefined);
  }

  const list = targets ?? [];
  const single = list.length === 1 ? list[0] : null;
  const effects = revokeEffects(list, servers);

  async function submit() {
    setBusy(true);
    setError(undefined);
    const done: string[] = [];
    let failure: string | undefined;
    for (const target of list) {
      try {
        await reauth.run(() => api<void>("admin", deletePath(target), { method: "DELETE", body: { reason: reason.trim() } }));
        done.push(targetId(target));
      } catch (e) {
        failure = e instanceof ReauthCancelled ? t("failCancelled") : e instanceof ApiError ? e.message : t("failGeneric");
        break;
      }
    }
    setBusy(false);
    if (done.length > 0) onRevoked(done);
    if (failure) setError(done.length > 0 ? t("failPartial", { failure, done: done.length, total: list.length }) : failure);
  }

  return (
    <>
    <RevokeDialogShell
      open={targets !== null}
      title={single ? t("dlgTitleSingle") : t("dlgTitleMany", { n: list.length })}
      description={
        single
          ? single.kind === "shared"
            ? t("dlgSingleShared", { server: single.server })
            : t("dlgSinglePersonal", { email: single.email, server: single.server })
          : t("dlgMany")
      }
      busy={busy}
      error={error}
      reason={reason}
      onReasonChange={setReason}
      onConfirm={() => void submit()}
      onClose={onClose}
    >
      {!single && list.length > 0 ? (
        <ul className="scrollbar-none max-h-28 overflow-y-auto rounded-control border border-line bg-ink/50 px-3 py-2 text-xs text-fg-muted">
          {list.map((target) => (
            <li key={targetId(target)} className="truncate py-0.5">
              {describeTarget(target, t)}
            </li>
          ))}
        </ul>
      ) : null}

      <fieldset className="flex flex-col gap-2" disabled={busy}>
        <legend className="mb-1 text-[13px] font-semibold text-fg">{t("commonReasons")}</legend>
        {PRESET_REASONS.map((key) => (
          <OptionCard key={key} name="revoke-reason" value={t(key)} checked={reason === t(key)} onSelect={setReason} title={t(key)} />
        ))}
      </fieldset>

      {effects.length > 0 ? (
        <div className="flex flex-col gap-2 rounded-card border border-line bg-guide/60 p-3">
          <p className="text-[13px] font-semibold text-fg">{t("effectsTitle")}</p>
          {effects.map((e) => (
            <p key={e.text} className="flex items-start gap-2 text-xs leading-5 text-fg-muted">
              {e.tone === "warn" ? <TriangleAlert className="mt-0.5 size-3.5 shrink-0 text-warn" aria-hidden /> : <Info className="mt-0.5 size-3.5 shrink-0 text-signal-fg" aria-hidden />}
              {e.text}
            </p>
          ))}
        </div>
      ) : null}
    </RevokeDialogShell>
    {reauth.dialog}
    </>
  );
}
