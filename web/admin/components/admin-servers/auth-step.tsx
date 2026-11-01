// Ported from design/v5/src/components/admin-servers/auth-step.tsx on 2026-10-31.
"use client";

import type { FormEvent } from "react";

import type { AuthKind } from "../../lib/admin-types";
import { useT } from "../../lib/i18n";
import { serversWizard } from "../../lib/i18n/messages/serversWizard";
import { ServiceLogo } from "../brand/ServiceLogo";

import { CredentialEnvStep } from "./credential-env-step";
import { MethodCards } from "./method-cards";
import { OAuthStep } from "./oauth-step";
import { SetupGuide } from "./setup-guide";
import { TokenStep } from "./token-step";
import type { HeaderDraft, HeaderDraftErrors, OAuthDraft, OAuthDraftErrors } from "./wizard-state";

export const AUTH_FORM_ID = "server-auth-form";

export interface AuthStepProps {
  slug: string;
  /** Where the server lives: the URL host or the Docker image. */
  where: string;
  /** Docker (stdio) server: no method choice; a credential variable makes it a per-user token server. */
  stdio: boolean;
  method: AuthKind;
  onMethodChange: (method: AuthKind) => void;
  header: HeaderDraft;
  onHeaderChange: (draft: HeaderDraft) => void;
  headerErrors: HeaderDraftErrors;
  oauth: OAuthDraft;
  onOAuthChange: (draft: OAuthDraft) => void;
  oauthErrors: OAuthDraftErrors;
  disabled: boolean;
  onSubmit: () => void;
}

/** Step 2: the method cards, the form for the chosen method and the setup guide beside it. */
export function AuthStep(props: AuthStepProps) {
  const t = useT(serversWizard);
  const { slug, where, stdio, method, header, oauth, disabled } = props;

  function submit(event: FormEvent) {
    event.preventDefault();
    props.onSubmit();
  }

  return (
    <form id={AUTH_FORM_ID} noValidate onSubmit={submit} className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_18rem]">
      <div className="flex min-w-0 flex-col gap-5">
        <div className="flex items-center gap-3">
          <ServiceLogo slug={slug} name={slug} variant="catalog" size="md" />
          <div className="min-w-0">
            <p className="truncate font-mono text-sm font-semibold text-fg">{slug}</p>
            <p className="truncate font-mono text-xs text-fg-muted">{where}</p>
          </div>
        </div>

        {stdio ? <CredentialEnvStep config={header} onChange={props.onHeaderChange} errors={props.headerErrors} disabled={disabled} /> : <MethodCards value={method} onValueChange={props.onMethodChange} disabled={disabled} />}

        {!stdio && method === "header" ? <TokenStep config={header} onChange={props.onHeaderChange} errors={props.headerErrors} disabled={disabled} /> : null}
        {!stdio && method === "oauth" ? <OAuthStep config={oauth} onChange={props.onOAuthChange} errors={props.oauthErrors} serverName={slug} disabled={disabled} /> : null}
        {!stdio && method === "none" ? (
          <p className="rounded-card border border-line-strong bg-ink/50 p-4 text-[13px] leading-6 text-fg-muted">
            {t("authNoneBody")}
          </p>
        ) : null}
      </div>

      <SetupGuide method={method} stdio={stdio} credentialEnv={header.credentialEnv.trim()} serverName={slug} helpUrl={header.helpUrl} headerName={header.headerName} prefix={header.prefix} />
    </form>
  );
}
