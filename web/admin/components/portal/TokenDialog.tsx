// Ported from design/v5/src/components/portal/connect-token-dialog.tsx on 2026-10-31.
// Token and password live only in this component's state: never logged, stored or put in a URL.
"use client";

import { ChevronRight, Eye, EyeOff, ExternalLink, FlaskConical } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";

import { ActionRow, ConnectTitle, Note, PasswordConfirm, ProfileField, SharedConnectionNote } from "../portal-v5/connect-common";
import { describeConnectError } from "../portal-v5/connect-request";
import { SetupGuide } from "../portal-v5/setup-guide";
import { Button } from "../ui/button";
import { Dialog } from "../ui/dialog";
import { Input } from "../ui/input";
import { countLabel } from "../../hooks/countLabel";
import { api } from "../../lib/api";
import { connectionEnvBody, connectionEnvErrors, type ConnectionVar } from "../../lib/connection-env";
import { useT } from "../../lib/i18n";
import { portalConnect } from "../../lib/i18n/messages/portalConnect";
import { DEFAULT_PROFILE, credentialEnvNote, credentialWordsOf, isHttpsUrl, isValidProfile, profileQuery, tokenMatchesHint } from "../../lib/portal-types";
import { cn } from "../../lib/utils";
import { ConnectionEnvFields } from "./connection-env-fields";

interface TokenTestResult {
  tools: string[];
  account_label?: string | null;
}

type TestState =
  | { kind: "idle" }
  | { kind: "testing" }
  | { kind: "ok"; tools: number; account: string | null }
  | { kind: "format" }
  | { kind: "rejected"; message: string };

const EMPTY_SECRETS = { token: "", password: "" };

export interface TokenDialogProps {
  slug: string;
  name: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** "Everyone": admin only, asks for the password and writes the shared connection. */
  shared: boolean;
  helpUrl: string | null;
  formatHint: string | null;
  /** stdio server: the token goes to this process variable; there is no header and no test endpoint. */
  credentialEnv?: string | null;
  /** stdio server: variables each connection informs itself (e.g. PORTAINER_URL). */
  connectionEnv?: readonly ConnectionVar[];
  /** Each connection informs its own MCP URL, asked before the token. */
  connectionUrl?: boolean;
  /** Last 4 of the shared token this one would replace (admins only). */
  sharedLast4?: string | null;
  /** Name the connection starts with (personal only): "default", or the one the agent asked for. */
  profile?: string;
  /** Profiles the viewer already has here; typing one of them replaces it. */
  existingProfiles?: readonly string[];
  /** Called after the token is saved, with the profile it was saved under; the caller reloads the app. */
  onConnected: (profile: string) => void;
}

export function TokenDialog({ slug, name, open, onOpenChange, ...rest }: TokenDialogProps) {
  const t = useT(portalConnect);
  const description = rest.credentialEnv
    ? t("tokenDescriptionEnv", { note: credentialEnvNote(rest.credentialEnv) })
    : t("tokenDescriptionHeader", { name });
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      size="xl"
      title={<ConnectTitle slug={slug} name={name}>{t("tokenTitle", { name, label: credentialWordsOf(rest.credentialEnv).label.toLowerCase() })}</ConnectTitle>}
      description={description}
    >
      <TokenForm slug={slug} name={name} onCancel={() => onOpenChange(false)} {...rest} />
    </Dialog>
  );
}

type TokenFormProps = Omit<TokenDialogProps, "open" | "onOpenChange"> & { onCancel: () => void };

const NO_VARS: readonly ConnectionVar[] = [];

function TokenForm({ slug, name, shared, helpUrl, formatHint, credentialEnv = null, connectionEnv = NO_VARS, connectionUrl = false, sharedLast4, profile: initialProfile = DEFAULT_PROFILE, existingProfiles = [], onConnected, onCancel }: TokenFormProps) {
  const t = useT(portalConnect);
  const fieldId = useId();
  const inputRef = useRef<HTMLInputElement>(null);
  const urlRef = useRef<HTMLInputElement>(null);
  const [endpoint, setEndpoint] = useState("");
  const [secrets, setSecrets] = useState(EMPTY_SECRETS);
  const [profile, setProfile] = useState(initialProfile);
  const [isRevealed, setIsRevealed] = useState(false);
  const [test, setTest] = useState<TestState>({ kind: "idle" });
  const [isSaving, setIsSaving] = useState(false);
  const [saveError, setSaveError] = useState<{ passwordRefused: boolean; message: string } | null>(null);
  const [envValues, setEnvValues] = useState<Record<string, string>>({});
  const [showEnvErrors, setShowEnvErrors] = useState(false);

  // The one place secrets are dropped when the dialog unmounts (close, Esc, save).
  useEffect(
    () => () => {
      setSecrets(EMPTY_SECRETS);
      setEnvValues({});
    },
    [],
  );

  const hint = formatHint ?? "";
  const words = credentialWordsOf(credentialEnv);
  const token = secrets.token.trim();
  const help = isHttpsUrl(helpUrl) ? helpUrl : null;
  const rejected = test.kind === "format" || test.kind === "rejected";
  const canTest = credentialEnv === null;
  const isExisting = shared ? Boolean(sharedLast4) : existingProfiles.includes(profile);
  const env = connectionEnvBody(connectionEnv, envValues, isExisting);
  const envErrors = connectionEnvErrors(connectionEnv, envValues, isExisting);
  const hasEnvErrors = Object.keys(envErrors).length > 0;
  // An existing stdio connection may change only its variables: the saved token is kept.
  const keepsToken = !canTest && isExisting && env !== undefined;
  const hasToken = canTest ? test.kind === "ok" : token !== "" || keepsToken;
  const url = endpoint.trim();
  // A new connection must name its MCP URL; an existing one keeps the saved URL when blank.
  const hasUrl = !connectionUrl || url !== "" || isExisting;
  const urlBody = connectionUrl && url !== "" ? { url } : {};
  const canSave = hasToken && hasUrl && (shared ? secrets.password !== "" : isValidProfile(profile));

  async function runTest() {
    if (!hasUrl) {
      urlRef.current?.focus();
      return;
    }
    if (token === "" || !tokenMatchesHint(token, hint)) {
      setTest({ kind: "format" });
      inputRef.current?.focus();
      return;
    }
    setTest({ kind: "testing" });
    try {
      const result = await api<TokenTestResult>("portal", `/connections/${encodeURIComponent(slug)}/token/test${shared ? "" : profileQuery(profile)}`, {
        method: "POST",
        body: { token, ...urlBody },
      });
      setTest({ kind: "ok", tools: result.tools.length, account: result.account_label ?? null });
    } catch (error) {
      setTest({ kind: "rejected", message: describeConnectError(error).message });
    }
  }

  async function save() {
    if (hasEnvErrors) {
      setShowEnvErrors(true);
      return;
    }
    if (token !== "" && !tokenMatchesHint(token, hint)) {
      setTest({ kind: "format" });
      inputRef.current?.focus();
      return;
    }
    setIsSaving(true);
    setSaveError(null);
    try {
      await api<{ last4: string }>("portal", `/connections/${encodeURIComponent(slug)}/token${shared ? "" : profileQuery(profile)}`, {
        method: "PUT",
        body: shared ? { token, env, ...urlBody, shared: true, password: secrets.password } : { token, env, ...urlBody },
      });
      setSecrets(EMPTY_SECRETS);
      setEnvValues({});
      onConnected(shared ? DEFAULT_PROFILE : profile);
    } catch (error) {
      setSaveError(describeConnectError(error));
      setIsSaving(false);
    }
  }

  const hintId = `${fieldId}-hint`;
  const resultId = `${fieldId}-result`;

  return (
    <div className="grid gap-6 md:grid-cols-[minmax(0,1fr)_17.5rem]">
      <form
        className="flex min-w-0 flex-col gap-5"
        onSubmit={(event) => {
          event.preventDefault();
          if (test.kind === "testing" || isSaving) return;
          if (canSave) void save();
          else if (canTest && test.kind !== "ok") void runTest();
        }}
      >
        {shared ? (
          <SharedConnectionNote last4={sharedLast4} />
        ) : null}

        {shared ? null : <ProfileField value={profile} onValueChange={setProfile} existing={existingProfiles} />}

        {connectionUrl ? (
          <div className="flex flex-col gap-1.5">
            <label htmlFor={`${fieldId}-url`} className="text-[13px] font-semibold text-fg">
              {t("mcpAddress")}
            </label>
            <Input
              ref={urlRef}
              id={`${fieldId}-url`}
              type="url"
              inputMode="url"
              autoComplete="off"
              spellCheck={false}
              maxLength={2048}
              required={!isExisting}
              value={endpoint}
              placeholder="https://.../mcp"
              aria-describedby={`${fieldId}-url-hint`}
              onChange={(event) => {
                setEndpoint(event.target.value);
                if (test.kind !== "testing") setTest({ kind: "idle" });
              }}
              className="font-mono text-[13px]"
            />
            <p id={`${fieldId}-url-hint`} className="text-xs leading-[1.125rem] text-fg-muted">
              {isExisting ? `${t("urlKeepsSaved")} ` : ""}{t("urlRule")}
            </p>
          </div>
        ) : null}

        <div className="flex flex-col gap-2">
          <div className="flex items-baseline justify-between gap-3">
            <label htmlFor={fieldId} className="text-[13px] font-semibold text-fg">
              {words.label}
            </label>
            {help ? (
              <a href={help} target="_blank" rel="noopener noreferrer" className="focus-ring inline-flex items-center gap-1 rounded-[4px] text-xs font-medium text-signal-fg hover:underline">
                {t("howToGenerate")}
                <ExternalLink className="size-3" aria-hidden />
                <span className="sr-only">{t("opensNewTab")}</span>
              </a>
            ) : null}
          </div>

          <div className="flex flex-col gap-2 sm:flex-row">
            <div
              className={cn(
                "flex h-10 min-w-0 flex-1 items-center rounded-control border bg-ink/70 transition-[border-color,box-shadow] duration-150 ease-switch",
                "focus-within:border-signal focus-within:ring-3 focus-within:ring-signal/30",
                rejected ? "border-cut ring-3 ring-cut/20" : test.kind === "ok" ? "border-ok/60" : "border-fg-subtle hover:border-fg-muted",
              )}
            >
              <input
                ref={inputRef}
                id={fieldId}
                type={isRevealed ? "text" : "password"}
                autoComplete="new-password"
                spellCheck={false}
                value={secrets.token}
                placeholder={hint ? `${hint}...` : t(`paste_${words.kind}`)}
                aria-invalid={rejected || undefined}
                aria-describedby={`${hintId} ${resultId}`}
                onChange={(event) => {
                  const value = event.target.value;
                  setSecrets((current) => ({ ...current, token: value }));
                  if (test.kind !== "testing") setTest({ kind: "idle" });
                }}
                className="h-full min-w-0 flex-1 bg-transparent px-3 font-mono text-[13px] text-fg outline-none placeholder:text-fg-muted/70"
              />
              <button
                type="button"
                onClick={() => setIsRevealed((value) => !value)}
                aria-label={t(isRevealed ? "hideSecret" : "showSecret", { label: words.label.toLowerCase() })}
                aria-pressed={isRevealed}
                className="mr-1 grid size-8 shrink-0 cursor-pointer place-items-center rounded-[4px] text-fg-muted outline-none transition-colors duration-150 ease-switch hover:bg-white/8 hover:text-fg focus-visible:ring-2 focus-visible:ring-signal-fg"
              >
                {isRevealed ? <EyeOff className="size-4" aria-hidden /> : <Eye className="size-4" aria-hidden />}
              </button>
            </div>
            {canTest ? (
              <Button type="button" size="lg" variant="secondary" loading={test.kind === "testing"} onClick={() => void runTest()}>
                {test.kind === "testing" ? null : <FlaskConical aria-hidden />}
                {test.kind === "testing" ? t("testing") : t("test")}
              </Button>
            ) : null}
          </div>

          <p id={hintId} className="text-xs leading-[1.125rem] text-fg-muted">
            {hint ? (
              <>
                {t("startsWithCap")} <code className="font-mono text-fg">{hint}</code>.{" "}
              </>
            ) : null}
            {credentialEnv ? <>{credentialEnvNote(credentialEnv)} </> : null}
            {keepsToken ? <>{t(`keepsSaved_${words.kind}`)} </> : null}
            {t("afterSave")}
          </p>
        </div>

        <ConnectionEnvFields
          vars={connectionEnv}
          values={envValues}
          onValueChange={(varName, value) => setEnvValues((current) => ({ ...current, [varName]: value }))}
          errors={showEnvErrors ? envErrors : {}}
          isExisting={isExisting}
        />

        {shared ? <PasswordConfirm value={secrets.password} onValueChange={(password) => setSecrets((current) => ({ ...current, password }))} error={saveError?.passwordRefused ? saveError.message : undefined} /> : null}

        <div id={resultId} aria-live="polite" className="empty:hidden">
          <TestResult test={test} name={name} hint={hint} />
          {saveError && !saveError.passwordRefused ? (
            <Note tone="cut" title={t("saveFailed")}>
              {saveError.message}
            </Note>
          ) : null}
        </div>

        <details className="group rounded-card border border-line md:hidden">
          <summary className="focus-ring flex cursor-pointer list-none items-center gap-2 rounded-card px-3 py-2.5 text-[13px] font-semibold text-fg [&::-webkit-details-marker]:hidden">
            <ChevronRight className="size-4 text-fg-muted transition-transform duration-150 ease-switch group-open:rotate-90" aria-hidden />
            {t("setupGuide")}
          </summary>
          <SetupGuide serverName={name} helpUrl={helpUrl} formatHint={formatHint} credentialEnv={credentialEnv} className="rounded-t-none" />
        </details>

        <ActionRow hint={!canTest ? t(`validatedFirstUse_${words.kind}`) : test.kind === "ok" ? null : t("testBeforeSave")}>
          <Button type="button" variant="ghost" onClick={onCancel}>
            {t("cancel")}
          </Button>
          <Button type="submit" variant="primary" disabled={!canSave} loading={isSaving}>
            {shared ? t("saveForAll") : t("saveAndUse")}
          </Button>
        </ActionRow>
      </form>

      <SetupGuide serverName={name} helpUrl={helpUrl} formatHint={formatHint} credentialEnv={credentialEnv} className="hidden self-start md:flex" />
    </div>
  );
}

function TestResult({ test, name, hint }: { test: TestState; name: string; hint: string }) {
  const t = useT(portalConnect);
  switch (test.kind) {
    case "idle":
      return null;
    case "testing":
      return (
        <Note tone="info" title={t("testingTitle", { name })}>
          {t("testingBody")}
        </Note>
      );
    case "ok":
      return (
        <Note tone="ok" title={countLabel(t, "okTitle", test.tools)}>
          {test.account ? `${t("okAccount", { account: test.account })} ` : ""}{t("nothingSavedYet")}
        </Note>
      );
    case "format":
      return (
        <Note tone="cut" title={t("formatTitle", { name })}>
          {hint ? `${t("formatPrefix", { name, hint })} ` : ""}{t("formatBody")}
        </Note>
      );
    case "rejected":
      return (
        <Note tone="cut" title={test.message}>
          {t("rejectedBody")}
        </Note>
      );
  }
}
