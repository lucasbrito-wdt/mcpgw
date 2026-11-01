"use client";

import { Trash2, Users } from "lucide-react";
import { useState, type FormEvent } from "react";

import type { AuthKind, HeaderConfig, SharedConnection as SharedData } from "../../lib/admin-types";
import { api, ApiError } from "../../lib/api";
import { formatDateTime, maskToken } from "../../lib/format";
import { hasKey, useT } from "../../lib/i18n";
import { errors } from "../../lib/i18n/messages/errors";
import { serversForms } from "../../lib/i18n/messages/serversForms";
import { ReauthCancelled, useReauth } from "../admin/ReauthDialog";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Dialog } from "../ui/dialog";
import { Input } from "../ui/input";
import { useToast } from "../ui/toast";

import { Field, describedBy } from "./field";
import { startsWithPrefix, stripPrefix } from "./wizard-state";

export interface SharedConnectionProps {
  slug: string;
  authKind: AuthKind;
  /** `has_shared_connection` of the server summary: true even when the details could not be loaded. */
  exists: boolean;
  shared: SharedData | null;
  header: HeaderConfig | null | undefined;
  onChanged: () => void;
}

/** The shared connection: who set it and its last 4 characters, never the value. Only admins write it (reauth). */
export function SharedConnection({ slug, authKind, exists, shared, header, onChanged }: SharedConnectionProps) {
  const t = useT(serversForms);
  const tError = useT(errors);
  const toast = useToast();
  const reauth = useReauth();
  const [replacing, setReplacing] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const path = `/servers/${encodeURIComponent(slug)}`;
  const prefix = header?.prefix ?? "";
  const doubled = startsWithPrefix(value, prefix);
  const statusLabel = (status: string) => {
    const key = `scStatus_${status}`;
    return hasKey(serversForms, key) ? t(key) : status;
  };

  function closeReplace() {
    setReplacing(false);
    setValue("");
    setError(undefined);
  }

  async function save(event: FormEvent) {
    event.preventDefault();
    if (!value.trim()) return setError(t("scValueRequired"));
    if (doubled) return setError(t("scPrefixDoubled", { prefix: prefix.trim() }));
    setBusy(true);
    setError(undefined);
    try {
      await reauth.run(() => api<void>("admin", `${path}/header`, { method: "PUT", body: { value: value.trim() } }));
      toast(t("scSaved"), { tone: "ok" });
      closeReplace();
      onChanged();
    } catch (e) {
      if (!(e instanceof ReauthCancelled)) setError(e instanceof ApiError ? (e.fields?.value ?? e.message) : tError("generic"));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    setBusy(true);
    try {
      await reauth.run(() => api<void>("admin", `${path}/shared`, { method: "DELETE" }));
      toast(t("scRemoved"), { tone: "ok" });
      setRemoving(false);
      onChanged();
    } catch (e) {
      if (e instanceof ReauthCancelled) return;
      toast(e instanceof ApiError ? e.message : t("scRemoveFailed"), { tone: "cut" });
    } finally {
      setBusy(false);
    }
  }

  return (
    <section aria-label={t("scTitle")} className="flex flex-col gap-3 rounded-card border border-line bg-panel/70 p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 text-[13px]">
          <p className="flex items-center gap-2 font-semibold text-fg">
            <Users className="size-4 text-fg-muted" aria-hidden />
            {exists ? t("scTitle") : t("scNone")}
            {shared && shared.status !== "active" ? <Badge tone="warn">{statusLabel(shared.status)}</Badge> : null}
          </p>
          <p className="mt-1 text-fg-muted">
            {shared
              ? `${maskToken(shared.last4)}${shared.connected_by_email ? t("scBy", { email: shared.connected_by_email }) : ""}${shared.last_used_at ? t("scUsedAt", { date: formatDateTime(shared.last_used_at) }) : ""}`
              : exists
                ? t("scActiveNoDetails")
                : t("scNotConnectedNote")}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          {authKind === "header" ? (
            <Button onClick={() => setReplacing(true)}>{exists ? t("scReplace") : t("scDefine")}</Button>
          ) : null}
          {exists ? (
            <Button variant="danger" onClick={() => setRemoving(true)}>
              <Trash2 aria-hidden />
              {t("remove")}
            </Button>
          ) : null}
        </div>
      </div>
      <p className="text-xs text-fg-muted">{t("scVaultNote")}</p>

      <Dialog
        open={replacing}
        onOpenChange={(next) => !next && !busy && closeReplace()}
        title={exists ? t("scReplaceTitle") : t("scDefineTitle")}
        description={t("scDialogDesc")}
        footer={
          <>
            <Button variant="ghost" disabled={busy} onClick={closeReplace}>
              {t("cancel")}
            </Button>
            <Button variant="primary" type="submit" form="shared-form" loading={busy}>
              {t("save")}
            </Button>
          </>
        }
      >
        <form id="shared-form" noValidate onSubmit={save}>
          <Field
            id="shared-value"
            label={t("value")}
            hint={prefix ? t("scPrefixHint", { prefix: prefix.trim() }) : undefined}
            error={
              error ?? (doubled ? (
                <span className="flex flex-wrap items-center gap-x-2">
                  {t("scPrefixDoubled", { prefix: prefix.trim() })}
                  <Button variant="link" size="sm" className="h-auto text-xs" onClick={() => setValue(stripPrefix(value, prefix))}>
                    {t("stripPrefix")}
                  </Button>
                </span>
              ) : undefined)
            }
          >
            <Input id="shared-value" type="password" autoComplete="new-password" spellCheck={false} value={value} onChange={(e) => setValue(e.target.value)} disabled={busy} className="font-mono text-[13px]" aria-invalid={Boolean(error || doubled) || undefined} aria-describedby={describedBy("shared-value", { hint: Boolean(prefix), error: error || doubled })} />
          </Field>
        </form>
      </Dialog>

      <Dialog
        open={removing}
        onOpenChange={(next) => !next && !busy && setRemoving(false)}
        size="sm"
        title={t("scRemoveTitle")}
        description={t("scRemoveDesc")}
        footer={
          <>
            <Button variant="ghost" disabled={busy} onClick={() => setRemoving(false)}>
              {t("cancel")}
            </Button>
            <Button variant="danger" loading={busy} onClick={() => void remove()}>
              {t("scRemoveConfirm")}
            </Button>
          </>
        }
      />
      {reauth.dialog}
    </section>
  );
}
