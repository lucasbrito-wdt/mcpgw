// Ported from design/v5/src/components/admin-servers/add-server-wizard.tsx on 2026-10-31.
// Hash states (#new, #new-token, #new-oauth) became React state; the catalog step became a custom-server step (plan gap G4).
"use client";

import { Check } from "lucide-react";
import { useState } from "react";

import { authKindLabel, buildAddServerBody, type AuthKind } from "../../lib/admin-types";
import { api, ApiError } from "../../lib/api";
import { useT, type Translator } from "../../lib/i18n";
import { errors } from "../../lib/i18n/messages/errors";
import { adminWizard } from "../../lib/i18n/messages/adminWizard";
import { cn } from "../../lib/utils";
import { AUTH_FORM_ID, AuthStep } from "../admin-servers/auth-step";
import { ServerStep } from "../admin-servers/server-step";
import {
  EMPTY_HEADER,
  EMPTY_OAUTH,
  EMPTY_SERVER,
  hasErrors,
  headerDraftErrors,
  lastFour,
  oauthDraftErrors,
  remoteHeaderErrors,
  remoteServerErrors,
  serverDraftErrors,
  stdioAuthKind,
  toAddServerForm,
} from "../admin-servers/wizard-state";
import { Badge } from "../ui/badge";
import { Button } from "../ui/button";
import { Dialog } from "../ui/dialog";

import { ReauthCancelled, useReauth } from "./ReauthDialog";

type Phase = "form" | "creating" | "done";

const STEPS = ["server", "auth"] as const;

type T = Translator<typeof adminWizard.en>;

interface Created {
  slug: string;
  authKind: AuthKind;
  sharedLast4: string | null;
  /** Docker server: the variable each user's token is delivered in, when it has one. */
  credentialEnv: string | null;
  isStdio: boolean;
  /** The server exists but a secret write after it failed. */
  warning: string | null;
}

function Stepper({ step }: { step: 1 | 2 }) {
  const t = useT(adminWizard);
  return (
    <ol aria-label={t("stepsLabel")} className="flex items-center gap-3 text-[13px]">
      {STEPS.map((id, index) => {
        const number = index + 1;
        const current = number === step;
        return (
          <li key={id} aria-current={current ? "step" : undefined} className={cn("flex items-center gap-2", current ? "font-semibold text-fg" : "text-fg-muted")}>
            <span className={cn("grid size-5 place-items-center rounded-full font-mono text-[11px]", current ? "bg-signal text-white" : number < step ? "bg-ok-wash text-ok" : "ring-1 ring-fg-subtle ring-inset")}>
              {number < step ? <Check className="size-3" aria-label={t("stepDone")} /> : number}
            </span>
            {t(`step_${id}`)}
            {index < STEPS.length - 1 ? <span aria-hidden className="ml-1 h-px w-8 bg-line-strong" /> : null}
          </li>
        );
      })}
    </ol>
  );
}

function createdNextStep(created: Created, t: T): string {
  if (!created.isStdio) return t("nextProvider");
  if (created.credentialEnv) return t("nextEnv", { env: created.credentialEnv });
  return t("nextFixed");
}

function CreatedPanel({ created }: { created: Created }) {
  const t = useT(adminWizard);
  return (
    <div role="status" className="flex flex-col items-start gap-4 py-2">
      <span className="grid size-10 place-items-center rounded-full bg-ok-wash text-ok" aria-hidden>
        <Check className="size-5" />
      </span>
      <div>
        <p className="text-base font-semibold text-fg">
          <span className="font-mono">{created.slug}</span> {t("createdSuffix")}
        </p>
        <p className="mt-1 max-w-prose text-sm leading-6 text-fg-muted">
          {t("createdBody")} {createdNextStep(created, t)}
        </p>
      </div>
      {created.warning ? (
        <p role="alert" className="max-w-prose rounded-card bg-warn-wash p-3 text-[13px] leading-6 text-warn ring-1 ring-warn/30 ring-inset">
          {created.warning}
        </p>
      ) : null}
      <dl className="grid w-full gap-x-6 gap-y-2 rounded-card border border-line bg-ink/50 p-4 text-[13px] sm:grid-cols-[auto_1fr]">
        {created.isStdio ? (
          <>
            <dt className="text-fg-muted">{t("credentialEach")}</dt>
            <dd className="text-fg">{created.credentialEnv ? <>{t("tokenInVar")} <span className="font-mono">{created.credentialEnv}</span></> : t("noCredential")}</dd>
          </>
        ) : (
          <>
            <dt className="text-fg-muted">{t("authentication")}</dt>
            <dd>
              <Badge>{authKindLabel(created.authKind)}</Badge>
            </dd>
            <dt className="text-fg-muted">{t("sharedConnection")}</dt>
            <dd className="text-fg">
              {created.sharedLast4 ? <span className="font-mono">••••{created.sharedLast4}</span> : t("noShared")}
            </dd>
          </>
        )}
      </dl>
    </div>
  );
}

export interface AddServerWizardProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Called once the server exists, so the list can reload. */
  onCreated: (slug: string) => void;
}

/** "Adicionar servidor": step 1 names the server and where it runs, step 2 sets how the key arrives. */
export function AddServerWizard({ open, onOpenChange, onCreated }: AddServerWizardProps) {
  const t = useT(adminWizard);
  const tErrors = useT(errors);
  const reauth = useReauth();
  const [wasOpen, setWasOpen] = useState(false);
  const [step, setStep] = useState<1 | 2>(1);
  const [phase, setPhase] = useState<Phase>("form");
  const [server, setServer] = useState(EMPTY_SERVER);
  const [chosenMethod, setMethod] = useState<AuthKind>("header");
  const [header, setHeader] = useState(EMPTY_HEADER);
  const [oauth, setOAuth] = useState(EMPTY_OAUTH);
  const [tried, setTried] = useState({ server: false, auth: false });
  const [remote, setRemote] = useState<Record<string, string> | undefined>();
  const [created, setCreated] = useState<Created | null>(null);
  const [failure, setFailure] = useState<string | null>(null);

  // Every time the wizard opens it starts clean.
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) {
      setStep(1);
      setPhase("form");
      setServer(EMPTY_SERVER);
      setMethod("header");
      setHeader(EMPTY_HEADER);
      setOAuth(EMPTY_OAUTH);
      setTried({ server: false, auth: false });
      setRemote(undefined);
      setCreated(null);
      setFailure(null);
    }
  }

  const close = () => onOpenChange(false);
  const busy = phase === "creating";
  const isStdio = server.transport === "stdio";
  const method = isStdio ? stdioAuthKind(header.credentialEnv) : chosenMethod;
  const where = server.transport === "stdio" ? server.dockerImage.trim() : server.url.trim();

  const serverErrors = { ...(tried.server ? serverDraftErrors(server, method) : {}), ...stripEmpty(remoteServerErrors(remote)) };
  const headerErrors = { ...headerDraftErrors(header, server.env), ...stripEmpty(remoteHeaderErrors(remote)) };
  const oauthErrors = tried.auth ? oauthDraftErrors(oauth) : {};

  function next() {
    setTried((t) => ({ ...t, server: true }));
    setRemote(undefined);
    if (hasErrors(serverDraftErrors(server, method))) return;
    setStep(2);
  }

  async function writeSecrets(slug: string): Promise<string | null> {
    try {
      if (!isStdio && method === "header" && header.sharedToken.trim()) {
        await reauth.run(() => api<void>("admin", `/servers/${slug}/header`, { method: "PUT", body: { value: header.sharedToken.trim() } }));
      }
      if (method === "oauth" && oauth.clientMode === "own") {
        const body = { client_id: oauth.clientId.trim(), client_secret: oauth.clientSecret.trim() };
        await reauth.run(() => api<void>("admin", `/servers/${slug}/oauth-client`, { method: "PUT", body }));
      }
      return null;
    } catch (e) {
      const reason = e instanceof ReauthCancelled ? t("secretCancelled") : e instanceof ApiError ? e.message : t("secretUnexpected");
      return t("secretFailed", { reason });
    }
  }

  async function create() {
    setTried((t) => ({ ...t, auth: true }));
    if (hasErrors(headerDraftErrors(header, server.env)) && method === "header") return;
    if (hasErrors(oauthDraftErrors(oauth)) && method === "oauth") return;
    setPhase("creating");
    setRemote(undefined);
    setFailure(null);
    try {
      await reauth.run(() => api("admin", "/servers", { method: "POST", body: buildAddServerBody(toAddServerForm(server, method, header)) }));
    } catch (e) {
      setPhase("form");
      if (e instanceof ReauthCancelled) return setFailure(t("createCancelled"));
      if (!(e instanceof ApiError)) return setFailure(tErrors("generic"));
      setRemote(e.fields);
      const onStep1 = hasErrors(remoteServerErrors(e.fields));
      if (onStep1) setStep(1);
      if (!onStep1 && !hasErrors(remoteHeaderErrors(e.fields))) setFailure(e.status === 409 ? t("duplicateSlug") : e.message);
      return;
    }
    const warning = await writeSecrets(server.slug);
    const shared = !isStdio && method === "header" && header.sharedToken.trim() && !warning ? lastFour(header.sharedToken) : null;
    setCreated({ slug: server.slug, authKind: method, sharedLast4: shared, credentialEnv: isStdio && header.credentialEnv.trim() ? header.credentialEnv.trim() : null, isStdio, warning });
    setPhase("done");
    onCreated(server.slug);
  }

  const footer =
    phase === "done" ? (
      <Button variant="primary" size="lg" onClick={close}>
        {t("done")}
      </Button>
    ) : step === 2 ? (
      <>
        <span className="mr-auto text-xs text-fg-muted max-sm:w-full">
          {t("prefixBefore")} <code className="font-mono text-fg">{server.slug}.</code> {t("prefixAfter")}
        </span>
        <Button size="lg" disabled={busy} onClick={() => setStep(1)}>
          {t("back")}
        </Button>
        <Button variant="primary" size="lg" type="submit" form={AUTH_FORM_ID} loading={busy}>
          {busy ? t("creating") : t("create")}
        </Button>
      </>
    ) : (
      <>
        <Button size="lg" onClick={close}>
          {t("cancel")}
        </Button>
        <Button variant="primary" size="lg" onClick={next}>
          {t("next")}
        </Button>
      </>
    );

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={(value) => !value && !busy && close()}
        title={t("title")}
        description={t("description")}
        className="max-w-5xl gap-4 p-4 sm:gap-5 sm:p-6"
        footer={footer}
      >
        {phase === "done" && created ? (
          <CreatedPanel created={created} />
        ) : (
          <>
            <Stepper step={step} />
            {failure ? (
              <p role="alert" className="rounded-card bg-cut-wash p-3 text-[13px] text-cut ring-1 ring-cut/35 ring-inset">
                {failure}
              </p>
            ) : null}
            {step === 2 ? (
              <AuthStep
                slug={server.slug}
                where={where}
                stdio={isStdio}
                method={method}
                onMethodChange={(m) => {
                  setMethod(m);
                  setTried((t) => ({ ...t, auth: false }));
                }}
                header={header}
                onHeaderChange={setHeader}
                headerErrors={headerErrors}
                oauth={oauth}
                onOAuthChange={setOAuth}
                oauthErrors={oauthErrors}
                disabled={busy}
                onSubmit={create}
              />
            ) : (
              <ServerStep draft={server} onChange={setServer} errors={serverErrors} disabled={busy} />
            )}
          </>
        )}
      </Dialog>
      {reauth.dialog}
    </>
  );
}

/** Drops keys with no message so `{ ...local, ...remote }` never overwrites a local error with undefined. */
function stripEmpty<T extends object>(errors: T): Partial<T> {
  return Object.fromEntries(Object.entries(errors).filter(([, value]) => Boolean(value))) as Partial<T>;
}
