// Ported from design/v5/src/components/admin-servers/oauth-step.tsx on 2026-10-31.
// Scopes are omitted: the contract (PUT /servers/{slug}/oauth-client) carries only client_id and client_secret (plan gap G4).
"use client";

import { Check, Copy } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { useT } from "../../lib/i18n";
import { serversWizard } from "../../lib/i18n/messages/serversWizard";
import { Button } from "../ui/button";
import { Input } from "../ui/input";

import { Field, describedBy } from "./field";
import { OptionCard } from "./option-card";
import type { OAuthDraft, OAuthDraftErrors } from "./wizard-state";

export interface OAuthStepProps {
  config: OAuthDraft;
  onChange: (config: OAuthDraft) => void;
  /** Required-field errors, passed in only after a create attempt. */
  errors: OAuthDraftErrors;
  serverName: string;
  disabled?: boolean;
}

const COPIED_MS = 2000;
const CALLBACK_PATH = "/connect/callback";

function RedirectUri() {
  const t = useT(serversWizard);
  const [copied, setCopied] = useState(false);
  const timer = useRef(0);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const uri = `${window.location.origin}${CALLBACK_PATH}`;

  async function copy() {
    try {
      await navigator.clipboard.writeText(uri);
    } catch {
      /* clipboard blocked: the URI stays selectable in the field */
    }
    setCopied(true);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setCopied(false), COPIED_MS);
  }

  return (
    <Field id="redirect-uri" label={t("redirectUri")} hint={t("redirectHint")}>
      <div className="flex gap-2">
        <Input id="redirect-uri" readOnly value={uri} onFocus={(e) => e.currentTarget.select()} className="font-mono text-[13px]" aria-describedby="redirect-uri-hint" />
        <Button variant="secondary" size="lg" className="h-9 text-xs" onClick={copy} aria-label={t("copyAria")}>
          {copied ? <Check aria-hidden /> : <Copy aria-hidden />}
          {copied ? t("copied") : t("copy")}
        </Button>
      </div>
      <span role="status" className="sr-only">
        {copied ? t("copiedStatus") : ""}
      </span>
    </Field>
  );
}

/** Step 2, OAuth 2.0: client registered by mcpgw (DCR) or the admin's own, and the redirect URI to copy. */
export function OAuthStep({ config, onChange, errors, serverName, disabled }: OAuthStepProps) {
  const t = useT(serversWizard);
  const set = (patch: Partial<OAuthDraft>) => onChange({ ...config, ...patch });
  return (
    <div className="flex flex-col gap-4">
      <fieldset className="min-w-0" disabled={disabled}>
        <legend className="mb-2 text-[13px] font-semibold text-fg">{t("oauthLegend")}</legend>
        <div className="grid gap-2 sm:grid-cols-2">
          <OptionCard
            name="oauth-client"
            value="dcr"
            checked={config.clientMode === "dcr"}
            onSelect={() => set({ clientMode: "dcr" })}
            title={t("dcrTitle")}
            description={t("dcrDesc", { server: serverName })}
          />
          <OptionCard
            name="oauth-client"
            value="own"
            checked={config.clientMode === "own"}
            onSelect={() => set({ clientMode: "own" })}
            title={t("ownTitle")}
            description={t("ownDesc")}
          />
        </div>
      </fieldset>

      {config.clientMode === "own" ? (
        <div className="grid gap-4 sm:grid-cols-2">
          <Field id="client-id" label={t("clientId")} error={errors.clientId}>
            <Input id="client-id" value={config.clientId} onChange={(e) => set({ clientId: e.target.value })} disabled={disabled} autoComplete="off" spellCheck={false} className="font-mono text-[13px]" aria-invalid={Boolean(errors.clientId) || undefined} aria-describedby={describedBy("client-id", { error: errors.clientId })} />
          </Field>
          <Field id="client-secret" label={t("clientSecret")} error={errors.clientSecret} hint={t("secretHint")}>
            <Input id="client-secret" type="password" value={config.clientSecret} onChange={(e) => set({ clientSecret: e.target.value })} disabled={disabled} autoComplete="new-password" spellCheck={false} className="font-mono text-[13px]" aria-invalid={Boolean(errors.clientSecret) || undefined} aria-describedby={describedBy("client-secret", { hint: true, error: errors.clientSecret })} />
          </Field>
        </div>
      ) : null}

      <RedirectUri />
    </div>
  );
}
