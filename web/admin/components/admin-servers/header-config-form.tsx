"use client";

import { useState, type FormEvent } from "react";

import type { HeaderConfig, ServerDetail } from "../../lib/admin-types";
import { api, ApiError } from "../../lib/api";
import { useT } from "../../lib/i18n";
import { errors as errorMessages } from "../../lib/i18n/messages/errors";
import { serversForms } from "../../lib/i18n/messages/serversForms";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { useToast } from "../ui/toast";

import { Field, describedBy } from "./field";
import { remoteHeaderErrors } from "./wizard-state";

const EMPTY: HeaderConfig = { name: "Authorization", prefix: "", format_hint: "", help_url: "" };
const MONO = "font-mono text-[13px]";

export interface HeaderConfigFormProps {
  server: ServerDetail;
  onSaved: () => void;
}

/** Edits the header the gateway sends (name, prefix, expected format, help link) with `PATCH /servers/{slug}`. */
export function HeaderConfigForm({ server, onSaved }: HeaderConfigFormProps) {
  const t = useT(serversForms);
  const tError = useT(errorMessages);
  const toast = useToast();
  const saved = server.header ?? EMPTY;
  const [draft, setDraft] = useState<HeaderConfig>(saved);
  const [busy, setBusy] = useState(false);
  const [errors, setErrors] = useState<ReturnType<typeof remoteHeaderErrors>>({});
  const set = (patch: Partial<HeaderConfig>) => setDraft((d) => ({ ...d, ...patch }));
  const unchanged = JSON.stringify(draft) === JSON.stringify(saved);

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setErrors({});
    try {
      await api("admin", `/servers/${encodeURIComponent(server.slug)}`, { method: "PATCH", body: { header: draft } });
      toast(t("hcSaved"), { tone: "ok" });
      onSaved();
    } catch (e) {
      if (!(e instanceof ApiError)) return toast(tError("generic"), { tone: "cut" });
      // Plan gap G23: the server refuses a prefix the shared value already starts with.
      const fields = e.code === "prefix_already_in_value" ? { ...e.fields, header_prefix: t("hcPrefixInValue") } : e.fields;
      const mapped = remoteHeaderErrors(fields);
      setErrors(mapped);
      if (!Object.values(mapped).some(Boolean)) toast(e.message, { tone: "cut" });
    } finally {
      setBusy(false);
    }
  }

  return (
    <form noValidate onSubmit={submit} aria-label={t("hcTitle")} className="flex flex-col gap-4 rounded-card border border-line bg-panel/70 p-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="cfg-name" label={t("hcName")} error={errors.headerName}>
          <Input id="cfg-name" value={draft.name} onChange={(e) => set({ name: e.target.value })} disabled={busy} autoComplete="off" spellCheck={false} className={MONO} aria-invalid={Boolean(errors.headerName) || undefined} aria-describedby={describedBy("cfg-name", { error: errors.headerName })} />
        </Field>
        <Field id="cfg-prefix" label={t("hcPrefix")} optional error={errors.prefix} hint={t("hcPrefixHint")}>
          <Input id="cfg-prefix" value={draft.prefix} onChange={(e) => set({ prefix: e.target.value })} disabled={busy} autoComplete="off" spellCheck={false} className={MONO} aria-invalid={Boolean(errors.prefix) || undefined} aria-describedby={describedBy("cfg-prefix", { hint: true, error: errors.prefix })} />
        </Field>
        <Field id="cfg-format" label={t("hcFormat")} optional error={errors.format} hint={t("hcFormatHint")}>
          <Input id="cfg-format" value={draft.format_hint} onChange={(e) => set({ format_hint: e.target.value })} disabled={busy} autoComplete="off" spellCheck={false} className={MONO} aria-invalid={Boolean(errors.format) || undefined} aria-describedby={describedBy("cfg-format", { hint: true, error: errors.format })} />
        </Field>
        <Field id="cfg-help" label={t("hcHelp")} optional error={errors.helpUrl} hint={t("hcHelpHint")}>
          <Input id="cfg-help" type="url" value={draft.help_url} onChange={(e) => set({ help_url: e.target.value })} disabled={busy} autoComplete="off" spellCheck={false} className={MONO} aria-invalid={Boolean(errors.helpUrl) || undefined} aria-describedby={describedBy("cfg-help", { hint: true, error: errors.helpUrl })} />
        </Field>
      </div>
      <div>
        <Button variant="primary" type="submit" loading={busy} disabled={unchanged}>
          {t("hcSave")}
        </Button>
      </div>
    </form>
  );
}
