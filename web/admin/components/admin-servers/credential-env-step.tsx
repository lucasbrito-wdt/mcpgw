"use client";

import { useT } from "../../lib/i18n";
import { serversWizard } from "../../lib/i18n/messages/serversWizard";
import { Input } from "../ui/input";

import { Field, describedBy } from "./field";
import type { HeaderDraft, HeaderDraftErrors } from "./wizard-state";

const MONO = "font-mono text-[13px]";

export interface CredentialEnvStepProps {
  config: HeaderDraft;
  onChange: (config: HeaderDraft) => void;
  errors: HeaderDraftErrors;
  disabled?: boolean;
}

/** Step 2 of a Docker server: the optional variable each user's own token is delivered in, plus the portal hints. */
export function CredentialEnvStep({ config, onChange, errors, disabled }: CredentialEnvStepProps) {
  const t = useT(serversWizard);
  const set = (patch: Partial<HeaderDraft>) => onChange({ ...config, ...patch });
  const hasCredential = config.credentialEnv.trim() !== "";
  return (
    <div className="flex flex-col gap-4">
      <Field id="credential-env" label={t("credLabel")} optional error={errors.credentialEnv} hint={t("credHint")}>
        <Input id="credential-env" name="credential-env-name" value={config.credentialEnv} onChange={(e) => set({ credentialEnv: e.target.value })} disabled={disabled} autoComplete="off" autoCapitalize="characters" spellCheck={false} placeholder="PORTAINER_API_KEY" className={MONO} aria-invalid={Boolean(errors.credentialEnv) || undefined} aria-describedby={describedBy("credential-env", { hint: true, error: errors.credentialEnv })} />
      </Field>
      {hasCredential ? (
        <Field id="connection-env" label={t("connLabel")} optional error={errors.connectionEnv} hint={t("connHint")}>
          <Input id="connection-env" name="connection-env-names" value={config.connectionEnv} onChange={(e) => set({ connectionEnv: e.target.value })} disabled={disabled} autoComplete="off" autoCapitalize="characters" spellCheck={false} placeholder="PORTAINER_URL" className={MONO} aria-invalid={Boolean(errors.connectionEnv) || undefined} aria-describedby={describedBy("connection-env", { hint: true, error: errors.connectionEnv })} />
        </Field>
      ) : null}
      {hasCredential ? (
        <div className="grid gap-4 sm:grid-cols-2">
          <Field id="credential-format" label={t("format")} optional error={errors.format} hint={t("formatHint")}>
            <Input id="credential-format" value={config.format} onChange={(e) => set({ format: e.target.value })} disabled={disabled} autoComplete="off" spellCheck={false} className={MONO} aria-invalid={Boolean(errors.format) || undefined} aria-describedby={describedBy("credential-format", { hint: true, error: errors.format })} />
          </Field>
          <Field id="credential-help" label={t("help")} optional error={errors.helpUrl} hint={t("helpHint")}>
            <Input id="credential-help" type="url" value={config.helpUrl} onChange={(e) => set({ helpUrl: e.target.value })} disabled={disabled} autoComplete="off" spellCheck={false} className={MONO} aria-invalid={Boolean(errors.helpUrl) || undefined} aria-describedby={describedBy("credential-help", { hint: true, error: errors.helpUrl })} />
          </Field>
        </div>
      ) : (
        <p className="rounded-card border border-line-strong bg-ink/50 p-4 text-[13px] leading-6 text-fg-muted">
          {t("noCredential")}
        </p>
      )}
    </div>
  );
}
