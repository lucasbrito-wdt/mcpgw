"use client";

import { useState, type FormEvent } from "react";

import type { ServerDetail } from "../../lib/admin-types";
import { api, ApiError } from "../../lib/api";
import { connectionEnvNamesError, parseEnvNames } from "../../lib/connection-env";
import { useT } from "../../lib/i18n";
import { errors } from "../../lib/i18n/messages/errors";
import { serversForms } from "../../lib/i18n/messages/serversForms";
import { ReauthCancelled, useReauth } from "../admin/ReauthDialog";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { useToast } from "../ui/toast";

import { Field, describedBy } from "./field";

/** Per-connection variables of a stdio server with a per-user token: names only, values live in each connection. */
export function ConnectionEnvForm({ server, onSaved }: { server: ServerDetail; onSaved: () => void }) {
  const t = useT(serversForms);
  const tError = useT(errors);
  const toast = useToast();
  const reauth = useReauth();
  const [raw, setRaw] = useState((server.connection_env ?? []).join(", "));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const credentialEnv = server.credential_env ?? "";
  const fixed = new Set(server.stdio_env_names ?? []);

  async function submit(event: FormEvent) {
    event.preventDefault();
    const invalid = connectionEnvNamesError(raw, credentialEnv);
    if (invalid) return setError(invalid);
    setBusy(true);
    setError(undefined);
    try {
      const body = { connection_env: parseEnvNames(raw) };
      await reauth.run(() => api<void>("admin", `/servers/${encodeURIComponent(server.slug)}`, { method: "PATCH", body }));
      toast(t("ceSaved"), { tone: "ok" });
      onSaved();
    } catch (e) {
      if (!(e instanceof ReauthCancelled)) setError(e instanceof ApiError ? (e.fields?.connection_env ?? e.message) : tError("generic"));
    } finally {
      setBusy(false);
    }
  }

  const names = server.connection_env ?? [];
  return (
    <form noValidate onSubmit={submit} aria-label={t("ceTitle")} className="flex flex-col gap-4 rounded-card border border-line bg-panel/70 p-4">
      <div>
        <h3 className="text-[13px] font-semibold text-fg">{t("ceTitle")}</h3>
        <p className="mt-1 text-xs text-fg-muted">
          {names.length === 0
            ? t("ceNone")
            : names.map((name) => `${name} ${fixed.has(name) ? t("ceDefault") : t("ceRequired")}`).join(" · ")}
        </p>
      </div>
      <Field id="connection-env-edit" label={t("ceNames")} optional error={error} hint={t("ceHint")}>
        <Input id="connection-env-edit" value={raw} onChange={(e) => setRaw(e.target.value)} disabled={busy} autoComplete="off" autoCapitalize="characters" spellCheck={false} placeholder="PORTAINER_URL" className="font-mono text-[13px]" aria-invalid={Boolean(error) || undefined} aria-describedby={describedBy("connection-env-edit", { hint: true, error })} />
      </Field>
      <div>
        <Button variant="primary" type="submit" loading={busy}>
          {t("ceSave")}
        </Button>
      </div>
      {reauth.dialog}
    </form>
  );
}
