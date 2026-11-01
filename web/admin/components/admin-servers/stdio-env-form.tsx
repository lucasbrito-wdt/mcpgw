"use client";

import { useState, type FormEvent } from "react";

import type { ServerDetail } from "../../lib/admin-types";
import { api, ApiError } from "../../lib/api";
import { useT } from "../../lib/i18n";
import { errors as errorMessages } from "../../lib/i18n/messages/errors";
import { serversForms } from "../../lib/i18n/messages/serversForms";
import { stdioEnvError, stdioEnvSetBody, stdioEnvUnsetBody, type StdioEnvFieldErrors } from "../../lib/stdio-env";
import { ReauthCancelled, useReauth } from "../admin/ReauthDialog";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { useToast } from "../ui/toast";

import { Field, describedBy } from "./field";

const MONO = "font-mono text-[13px]";

/** Fixed environment of a stdio server: names only ("valor no cofre"); add, replace or remove one variable at a time. */
export function StdioEnvForm({ server, onSaved }: { server: ServerDetail; onSaved: () => void }) {
  const t = useT(serversForms);
  const tError = useT(errorMessages);
  const toast = useToast();
  const reauth = useReauth();
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [errors, setErrors] = useState<StdioEnvFieldErrors>({});
  const [removeError, setRemoveError] = useState<string | undefined>();
  const names = server.stdio_env_names ?? [];
  const path = `/servers/${encodeURIComponent(server.slug)}`;

  async function save(body: object, done: string): Promise<boolean> {
    setBusy(true);
    try {
      await reauth.run(() => api<void>("admin", path, { method: "PATCH", body }));
      toast(done, { tone: "ok" });
      onSaved();
      return true;
    } catch (e) {
      if (!(e instanceof ReauthCancelled)) throw e;
      return false;
    } finally {
      setBusy(false);
    }
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    const invalid = stdioEnvError(name, value, server.credential_env ?? "");
    setErrors(invalid);
    if (invalid.name || invalid.value) return;
    try {
      if (await save(stdioEnvSetBody(name, value), t("seSaved", { name: name.trim() }))) {
        setName("");
        setValue("");
      }
    } catch (e) {
      setErrors({ value: e instanceof ApiError ? (e.fields?.stdio_env ?? e.message) : tError("generic") });
    }
  }

  async function remove(target: string) {
    setRemoveError(undefined);
    try {
      await save(stdioEnvUnsetBody(target), t("seRemoved", { name: target }));
    } catch (e) {
      setRemoveError(e instanceof ApiError ? (e.fields?.stdio_env ?? e.message) : tError("generic"));
    }
  }

  return (
    <form noValidate onSubmit={submit} aria-label={t("seTitle")} className="flex flex-col gap-4 rounded-card border border-line bg-panel/70 p-4">
      <div>
        <h3 className="text-[13px] font-semibold text-fg">{t("seTitle")}</h3>
        <p className="mt-1 text-xs text-fg-muted">{t("seDesc")}</p>
      </div>
      {names.length === 0 ? (
        <p className="text-[13px] text-fg-muted">{t("seNone")}</p>
      ) : (
        <ul className="flex flex-col divide-y divide-line">
          {names.map((n) => (
            <li key={n} className="flex items-center justify-between gap-3 py-1.5">
              <span className="min-w-0 break-all">
                <span className={MONO}>{n}</span>
                <span className="ml-2 text-xs text-fg-muted">{t("seValueInVault")}</span>
              </span>
              <Button variant="ghost" size="sm" type="button" disabled={busy} aria-label={t("seRemoveAria", { name: n })} onClick={() => void remove(n)} className="hover:text-cut">
                {t("remove")}
              </Button>
            </li>
          ))}
        </ul>
      )}
      {removeError ? (
        <p role="alert" className="text-xs leading-5 text-cut">
          {removeError}
        </p>
      ) : null}
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="stdio-env-name" label={t("seAddOrReplace")} error={errors.name} hint={t("seAddHint")}>
          <Input id="stdio-env-name" name="stdio-env-key" value={name} onChange={(e) => setName(e.target.value)} disabled={busy} autoComplete="off" autoCapitalize="characters" spellCheck={false} placeholder="PORTAINER_TIMEOUT" className={MONO} aria-invalid={Boolean(errors.name) || undefined} aria-describedby={describedBy("stdio-env-name", { hint: true, error: errors.name })} />
        </Field>
        <Field id="stdio-env-value" label={t("value")} error={errors.value}>
          {/* Secret: "new-password" is what stops Chrome from offering the gateway login here. */}
          <Input id="stdio-env-value" name="stdio-env-secret" type="password" value={value} onChange={(e) => setValue(e.target.value)} disabled={busy} autoComplete="new-password" data-1p-ignore data-lpignore="true" spellCheck={false} placeholder={t("seValuePlaceholder")} className={MONO} aria-invalid={Boolean(errors.value) || undefined} aria-describedby={describedBy("stdio-env-value", { error: errors.value })} />
        </Field>
      </div>
      <div>
        <Button variant="primary" type="submit" loading={busy}>
          {t("seSave")}
        </Button>
      </div>
      {reauth.dialog}
    </form>
  );
}
