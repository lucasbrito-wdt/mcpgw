// Ported from design/v5/src/components/portal/connect-oauth-dialog.tsx on 2026-10-31.
// Real flow: POST .../oauth returns the redirect URL, then the page leaves for the provider. The mock
// scopes list, the "denied" phase and the "back" phase are gone (not in the contract; the callback
// redirect lands on /portal/?connected=<slug>).
"use client";

import { ArrowLeftRight, LoaderCircle, ShieldCheck } from "lucide-react";
import { motion } from "motion/react";
import { useEffect, useRef, useState } from "react";

import { McpgwTile } from "../brand/McpgwMark";
import { ServiceLogo } from "../brand/ServiceLogo";
import { Button } from "../ui/button";
import { Dialog } from "../ui/dialog";
import { usePrefersReducedMotion } from "../../hooks/usePrefersReducedMotion";
import { api } from "../../lib/api";
import { useT } from "../../lib/i18n";
import { portalConnect } from "../../lib/i18n/messages/portalConnect";
import { DURATION, EASE_SWITCH } from "../../lib/motion";
import { DEFAULT_PROFILE, isValidProfile, profileQuery } from "../../lib/portal-types";
import { cn } from "../../lib/utils";

import { ActionRow, ConnectTitle, Note, PasswordConfirm, ProfileField, SharedConnectionNote } from "./connect-common";
import { describeConnectError } from "./connect-request";

export interface ConnectOAuthDialogProps {
  slug: string;
  name: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** "Everyone": admin only, asks for the password; the returned URL carries a 5 minute ticket. */
  shared: boolean;
  /** Last 4 of the shared token this one would replace (admins only). */
  sharedLast4?: string | null;
  /** Name the connection starts with (personal only). */
  profile?: string;
  /** Profiles the viewer already has here; typing one of them replaces it. */
  existingProfiles?: readonly string[];
}

export function ConnectOAuthDialog({ slug, name, open, onOpenChange, shared, sharedLast4, profile, existingProfiles }: ConnectOAuthDialogProps) {
  const t = useT(portalConnect);
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      size="lg"
      title={<ConnectTitle slug={slug} name={name}>{t("oauthTitle", { name })}</ConnectTitle>}
      description={t("oauthDescription")}
    >
      <OAuthFlow slug={slug} name={name} shared={shared} sharedLast4={sharedLast4} profile={profile} existingProfiles={existingProfiles} onCancel={() => onOpenChange(false)} />
    </Dialog>
  );
}

interface OAuthFlowProps {
  slug: string;
  name: string;
  shared: boolean;
  sharedLast4?: string | null;
  profile?: string;
  existingProfiles?: readonly string[];
  onCancel: () => void;
}

function isWebUrl(value: string): boolean {
  try {
    return ["http:", "https:"].includes(new URL(value, window.location.origin).protocol);
  } catch {
    return false;
  }
}

function OAuthFlow({ slug, name, shared, sharedLast4, profile: initialProfile = DEFAULT_PROFILE, existingProfiles = [], onCancel }: OAuthFlowProps) {
  const t = useT(portalConnect);
  const [isLeaving, setIsLeaving] = useState(false);
  const [password, setPassword] = useState("");
  const [profile, setProfile] = useState(initialProfile);
  const [error, setError] = useState<{ passwordRefused: boolean; message: string } | null>(null);
  const isOpen = useRef(true);
  useEffect(() => {
    isOpen.current = true;
    return () => {
      isOpen.current = false;
    };
  }, []);

  const canStart = shared ? password !== "" : isValidProfile(profile);

  async function start() {
    setError(null);
    setIsLeaving(true);
    try {
      const result = await api<{ redirect_url: string }>("portal", `/connections/${encodeURIComponent(slug)}/oauth${shared ? "" : profileQuery(profile)}`, {
        method: "POST",
        body: shared ? { shared: true, password } : {},
      });
      if (!isWebUrl(result.redirect_url)) throw new Error("redirect");
      setPassword("");
      // Cancel closed the dialog while the request was in flight: stay on the page.
      if (isOpen.current) window.location.assign(result.redirect_url);
    } catch (caught) {
      setIsLeaving(false);
      setError(describeConnectError(caught));
    }
  }

  if (isLeaving) return <Journey slug={slug} name={name} onCancel={onCancel} />;

  return (
    <form
      className="flex flex-col gap-5"
      onSubmit={(event) => {
        event.preventDefault();
        if (canStart) void start();
      }}
    >
      {shared ? (
        <SharedConnectionNote last4={sharedLast4} />
      ) : null}

      {shared ? null : <ProfileField value={profile} onValueChange={setProfile} existing={existingProfiles} />}

      {shared ? <PasswordConfirm value={password} onValueChange={setPassword} error={error?.passwordRefused ? error.message : undefined} /> : null}

      {error && !error.passwordRefused ? (
        <Note tone="cut" title={t("oauthStartFailed")}>
          {error.message}
        </Note>
      ) : null}

      <ActionRow hint={t("oauthHint")}>
        <Button type="button" variant="ghost" onClick={onCancel}>
          {t("cancel")}
        </Button>
        <Button type="submit" variant="primary" disabled={!canStart}>
          {t("continueOn", { name })}
        </Button>
      </ActionRow>
    </form>
  );
}

const STEPS = ["stepLeave", "stepAuthorize", "stepReturn"] as const;

/** The round trip as a three-stop line: mcpgw, the provider, back. Shown while the page leaves for the provider. */
function Journey({ slug, name, onCancel }: { slug: string; name: string; onCancel: () => void }) {
  const t = useT(portalConnect);
  const reduceMotion = usePrefersReducedMotion();

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center justify-center gap-3 rounded-card border border-line bg-black/40 px-4 py-8 sm:gap-5">
        <McpgwTile className="size-12" markClassName="size-6" />
        <span className="relative h-0.5 w-16 overflow-hidden rounded-full bg-line-strong sm:w-28" aria-hidden>
          <motion.span
            className="absolute inset-y-0 w-1/3 rounded-full bg-core"
            initial={{ x: "-100%" }}
            animate={{ x: reduceMotion ? "100%" : "300%" }}
            transition={reduceMotion ? { duration: 0 } : { duration: DURATION.draw * 1.4, ease: EASE_SWITCH, repeat: Infinity }}
          />
        </span>
        <span className="relative">
          <ServiceLogo slug={slug} name={name} variant="catalog" size="lg" />
          <ArrowLeftRight className="absolute -right-2 -bottom-2 size-5 rounded-full bg-panel p-1 text-fg-muted ring-1 ring-line" aria-hidden />
        </span>
      </div>

      <div aria-live="polite" className="flex flex-col items-center gap-1 text-center">
        <p className="flex items-center gap-2 text-sm font-semibold text-fg">
          <LoaderCircle className="size-4 shrink-0 animate-spin text-signal-fg motion-reduce:animate-none" aria-hidden />
          {t("opening", { name })}
        </p>
        <p className="text-[13px] text-fg-muted">{t("authorizeOn", { name })}</p>
      </div>

      <ol className="grid grid-cols-3 gap-2" aria-label={t("stepsLabel")}>
        {STEPS.map((step, index) => (
          <li key={step} aria-current={index === 0 ? "step" : undefined} className="flex flex-col gap-2">
            <span className={cn("h-1 rounded-full", index === 0 ? "bg-signal-fg" : "bg-line-strong")} aria-hidden />
            <span className={cn("text-xs", index === 0 ? "text-fg" : "text-fg-muted")}>{t(step)}</span>
          </li>
        ))}
      </ol>

      <div className="flex items-center justify-between gap-3 border-t border-line pt-4">
        <p className="flex items-center gap-1.5 text-xs text-fg-muted">
          <ShieldCheck className="size-3.5 text-ok" aria-hidden />
          {t("neverSees", { name })}
        </p>
        <Button variant="ghost" onClick={onCancel}>
          {t("cancel")}
        </Button>
      </div>
    </div>
  );
}
