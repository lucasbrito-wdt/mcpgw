// Ported from design/v5/src/components/admin-servers/token-step.tsx on 2026-10-31.
"use client";

import { Eraser } from "lucide-react";

import { useT } from "../../lib/i18n";
import { serversWizard } from "../../lib/i18n/messages/serversWizard";
import { Button } from "../ui/button";
import { Input } from "../ui/input";

import { Field, describedBy } from "./field";
import { lastFour, startsWithPrefix, stripPrefix, type HeaderDraft, type HeaderDraftErrors } from "./wizard-state";

const MASK = "••••••••";

export interface TokenStepProps {
  config: HeaderDraft;
  onChange: (config: HeaderDraft) => void;
  errors: HeaderDraftErrors;
  disabled?: boolean;
}

/**
 * The header line every call leaves with. The value is always masked: a pasted token is write-only,
 * so the preview shows at most its last 4 characters. A prefix already inside the value shows doubled, in red.
 */
function HeaderPreview({ config }: { config: HeaderDraft }) {
  const t = useT(serversWizard);
  const duplicated = startsWithPrefix(config.sharedToken, config.prefix);
  const tail = config.sharedToken.trim() ? lastFour(config.sharedToken) : "";
  return (
    <div className="rounded-card border border-line-strong bg-ink/70 p-3">
      <p className="text-xs text-fg-muted">{t("previewTitle")}</p>
      <p aria-live="polite" className="mt-1.5 font-mono text-[13px] leading-6 break-all whitespace-pre-wrap text-fg">
        <span>{config.headerName.trim() || "Authorization"}</span>
        <span className="text-fg-muted">: </span>
        {config.prefix ? <span className="text-signal-fg">{config.prefix}</span> : null}
        {duplicated ? <span className="rounded-[3px] bg-cut-wash text-cut">{config.prefix}</span> : null}
        <span>
          {MASK}
          {tail}
        </span>
        {!tail ? <span className="ml-2 font-sans text-xs text-fg-muted">{t("previewTokenNote")}</span> : null}
      </p>
    </div>
  );
}

/** Step 2, Header: name, prefix, expected format, help link, live preview and the optional shared value. */
export function TokenStep({ config, onChange, errors, disabled }: TokenStepProps) {
  const t = useT(serversWizard);
  const tokenError = errors.sharedToken;
  const set = (patch: Partial<HeaderDraft>) => onChange({ ...config, ...patch });
  const mono = "font-mono text-[13px]";

  return (
    <div className="flex flex-col gap-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="header-name" label={t("headerName")} error={errors.headerName}>
          <Input id="header-name" value={config.headerName} onChange={(e) => set({ headerName: e.target.value })} disabled={disabled} autoComplete="off" spellCheck={false} className={mono} aria-invalid={Boolean(errors.headerName) || undefined} aria-describedby={describedBy("header-name", { error: errors.headerName })} />
        </Field>
        <Field id="header-prefix" label={t("prefix")} optional error={errors.prefix} hint={t("prefixHint")}>
          <Input id="header-prefix" value={config.prefix} onChange={(e) => set({ prefix: e.target.value })} disabled={disabled} autoComplete="off" spellCheck={false} className={mono} aria-invalid={Boolean(errors.prefix) || undefined} aria-describedby={describedBy("header-prefix", { hint: true, error: errors.prefix })} />
        </Field>
        <Field id="header-format" label={t("format")} optional error={errors.format} hint={t("formatHint")}>
          <Input id="header-format" value={config.format} onChange={(e) => set({ format: e.target.value })} disabled={disabled} autoComplete="off" spellCheck={false} placeholder={t("formatPlaceholder")} className={mono} aria-invalid={Boolean(errors.format) || undefined} aria-describedby={describedBy("header-format", { hint: true, error: errors.format })} />
        </Field>
        <Field id="header-help" label={t("help")} optional error={errors.helpUrl} hint={t("helpHint")}>
          <Input id="header-help" type="url" value={config.helpUrl} onChange={(e) => set({ helpUrl: e.target.value })} disabled={disabled} autoComplete="off" spellCheck={false} className={mono} aria-invalid={Boolean(errors.helpUrl) || undefined} aria-describedby={describedBy("header-help", { hint: true, error: errors.helpUrl })} />
        </Field>
      </div>

      <HeaderPreview config={config} />

      <Field
        id="shared-token"
        label={t("sharedLabel")}
        optional
        hint={t("sharedHint")}
        error={
          tokenError ? (
            <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <span>{tokenError.message}</span>
              <Button variant="link" size="sm" className="h-auto text-xs" onClick={() => set({ sharedToken: stripPrefix(config.sharedToken, config.prefix) })}>
                <Eraser aria-hidden />
                {t("stripPrefix")}
              </Button>
            </span>
          ) : undefined
        }
      >
        <Input
          id="shared-token"
          type="password"
          value={config.sharedToken}
          onChange={(e) => set({ sharedToken: e.target.value })}
          disabled={disabled}
          autoComplete="new-password"
          spellCheck={false}
          placeholder={t("sharedPlaceholder")}
          className={mono}
          aria-invalid={Boolean(tokenError) || undefined}
          aria-describedby={describedBy("shared-token", { hint: true, error: tokenError })}
        />
      </Field>
    </div>
  );
}
