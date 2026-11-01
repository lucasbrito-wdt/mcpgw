// Ported from design/v5/src/components/portal/connect-common.tsx on 2026-10-31.
// MethodSwitch is not ported: the contract gives one auth_kind per server (oauth or header).
"use client";

import { CircleCheck, CircleX, Info, type LucideIcon } from "lucide-react";
import { useId, type ReactNode } from "react";

import { ServiceLogo } from "../brand/ServiceLogo";
import { Input } from "../ui/input";
import { useT } from "../../lib/i18n";
import { portalConnect } from "../../lib/i18n/messages/portalConnect";
import { DEFAULT_PROFILE, isValidProfile } from "../../lib/portal-types";
import { cn } from "../../lib/utils";

export type NoteTone = "ok" | "cut" | "info";

const NOTE: Record<NoteTone, { icon: LucideIcon; box: string; tint: string }> = {
  ok: { icon: CircleCheck, box: "border-ok/30 bg-ok-wash", tint: "text-ok" },
  cut: { icon: CircleX, box: "border-cut/35 bg-cut-wash", tint: "text-cut" },
  info: { icon: Info, box: "border-line-strong bg-white/4", tint: "text-signal-fg" },
};

/** Inline result of a check (token tested, OAuth refused): what happened and what to do. */
export function Note({ tone, title, children, className }: { tone: NoteTone; title: ReactNode; children?: ReactNode; className?: string }) {
  const look = NOTE[tone];
  const Icon = look.icon;
  return (
    <div className={cn("flex gap-2.5 rounded-card border p-3", look.box, className)}>
      <Icon className={cn("mt-0.5 size-4 shrink-0", look.tint)} aria-hidden />
      <div className="min-w-0 text-[13px] leading-5">
        <p className="font-semibold text-fg">{title}</p>
        {children ? <div className="mt-0.5 text-pretty text-fg-muted">{children}</div> : null}
      </div>
    </div>
  );
}

/** Explains a connection that serves everybody; says which shared one it would replace. */
export function SharedConnectionNote({ last4 }: { last4?: string | null }) {
  const t = useT(portalConnect);
  const body = [t("sharedNoteBody"), last4 ? t("sharedReplaces", { last4 }) : null, t("sharedAudit")];
  return (
    <Note tone="info" title={t("sharedNoteTitle")}>
      {body.filter(Boolean).join(" ")}
    </Note>
  );
}

/** Dialog title with the server logo, like the Airbyte connector header. */
export function ConnectTitle({ slug, name, children }: { slug: string; name: string; children: ReactNode }) {
  return (
    <span className="flex items-center gap-3">
      <ServiceLogo slug={slug} name={name} variant="catalog" size="sm" />
      <span className="min-w-0">{children}</span>
    </span>
  );
}

/** Bottom action row inside a dialog body (primary last). */
export function ActionRow({ children, hint }: { children: ReactNode; hint?: ReactNode }) {
  return (
    <div className="flex flex-col-reverse gap-3 border-t border-line pt-4 sm:flex-row sm:items-center sm:justify-between">
      <p className="text-xs text-fg-muted">{hint}</p>
      <div className="flex flex-wrap items-center justify-end gap-2">{children}</div>
    </div>
  );
}

export interface PasswordConfirmProps {
  value: string;
  onValueChange: (value: string) => void;
  /** Message under the field when the server refused the password (reauth_required). */
  error?: string;
}

/** Admin password, asked again before a credential that serves everybody (plan gap G20). Lives only in state. */
export function PasswordConfirm({ value, onValueChange, error }: PasswordConfirmProps) {
  const t = useT(portalConnect);
  const id = useId();
  return (
    <div className="flex flex-col gap-2 rounded-card border border-warn/30 bg-warn-wash p-3">
      <label htmlFor={id} className="text-[13px] font-semibold text-fg">
        {t("passwordConfirmLabel")}
      </label>
      <Input
        id={id}
        type="password"
        autoComplete="current-password"
        value={value}
        aria-invalid={error ? true : undefined}
        aria-describedby={`${id}-hint`}
        onChange={(event) => onValueChange(event.target.value)}
      />
      <p id={`${id}-hint`} className={cn("text-xs leading-[1.125rem]", error ? "text-cut" : "text-fg-muted")}>
        {error ?? t("passwordConfirmHint")}
      </p>
    </div>
  );
}

export interface ProfileFieldProps {
  value: string;
  onValueChange: (value: string) => void;
  /** Profiles the viewer already has on this server: typing one of them replaces it. */
  existing: readonly string[];
}

/** Name of the personal connection ("default" unless the viewer keeps several accounts on one server). */
export function ProfileField({ value, onValueChange, existing }: ProfileFieldProps) {
  const t = useT(portalConnect);
  const id = useId();
  const isInvalid = value !== "" && !isValidProfile(value);
  const replaces = existing.includes(value);
  let message = t("profileInvalid");
  if (value === "") message = t("profileEmpty");
  else if (!isInvalid) {
    message = replaces ? t("profileReplaces", { name: value }) : value === DEFAULT_PROFILE ? t("profileDefault") : t("profileNamed");
  }
  return (
    <div className="flex flex-col gap-2">
      <label htmlFor={id} className="text-[13px] font-semibold text-fg">
        {t("profileName")}
      </label>
      <Input
        id={id}
        value={value}
        maxLength={32}
        autoComplete="off"
        spellCheck={false}
        aria-invalid={isInvalid || undefined}
        aria-describedby={`${id}-hint`}
        onChange={(event) => onValueChange(event.target.value.trim())}
        className="font-mono"
      />
      <p id={`${id}-hint`} className={cn("text-xs leading-[1.125rem]", isInvalid ? "text-cut" : replaces ? "text-warn" : "text-fg-muted")}>
        {message}
      </p>
    </div>
  );
}
