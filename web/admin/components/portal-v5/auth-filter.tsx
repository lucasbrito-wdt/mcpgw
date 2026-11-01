// Ported from design/v5/src/app/portal/_components/auth-filter.tsx on 2026-10-31.
// Single choice, not a checkbox set: the portal API takes one `auth` value and filters on the server.
"use client";

import { ChevronDown, KeyRound } from "lucide-react";
import { useId, useState } from "react";

import { Button } from "../ui/button";
import { Popover } from "../ui/popover";
import { useT } from "../../lib/i18n";
import { portalServers } from "../../lib/i18n/messages/portalServers";
import type { AuthKind } from "../../lib/portal-types";

/** "" means every method (no `auth` parameter). */
export type AuthFilterValue = "" | AuthKind;

type PortalKey = keyof typeof portalServers.en;

const OPTIONS: { value: AuthFilterValue; label: PortalKey; hint: PortalKey | null }[] = [
  { value: "", label: "authAll", hint: null },
  { value: "oauth", label: "authOauth", hint: "authOauthHint" },
  { value: "header", label: "authToken", hint: "authHeaderHint" },
  { value: "none", label: "authNone", hint: "authNoneHint" },
];

export function AuthFilter({ value, onValueChange }: { value: AuthFilterValue; onValueChange: (next: AuthFilterValue) => void }) {
  const t = useT(portalServers);
  const [open, setOpen] = useState(false);
  const name = useId();
  const current = OPTIONS.find((option) => option.value === value) ?? OPTIONS[0];
  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      title={t("authFilterTitle")}
      align="end"
      className="w-64"
      trigger={
        <Button variant="secondary" size="md" className={value !== "" ? "border-signal text-signal-fg" : undefined}>
          <KeyRound aria-hidden />
          <span className="max-sm:sr-only">{t("authentication")}</span>
          <span className="font-normal text-fg-muted">{t(current.label).toLowerCase()}</span>
          <ChevronDown aria-hidden className="text-fg-muted" />
        </Button>
      }
    >
      <fieldset className="flex flex-col">
        <legend className="sr-only">{t("authentication")}</legend>
        {OPTIONS.map((option) => (
          <label key={option.value} className="flex cursor-pointer items-center gap-2.5 rounded-control px-1.5 py-2 text-[13px] text-fg hover:bg-white/5">
            <input
              type="radio"
              name={name}
              checked={value === option.value}
              onChange={() => {
                onValueChange(option.value);
                setOpen(false);
              }}
              className="size-4 accent-signal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-signal-fg"
            />
            <span className="flex-1">{t(option.label)}</span>
            <span className="text-xs text-fg-muted">{option.hint ? t(option.hint) : ""}</span>
          </label>
        ))}
      </fieldset>
    </Popover>
  );
}
