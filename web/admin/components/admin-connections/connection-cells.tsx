// Ported from design/v5/src/components/admin-connections/connection-cells.tsx on 2026-10-31.
import { Check, Minus } from "lucide-react";
import type { ReactNode } from "react";

import { formatDateTime } from "../../lib/format";
import { getLocale, translate, useT } from "../../lib/i18n";
import { adminConnections } from "../../lib/i18n/messages/adminConnections";
import { cn } from "../../lib/utils";
import { ServiceLogo } from "../brand/ServiceLogo";
import { RouteLine } from "../signature/RouteLine";
import { Badge } from "../ui/badge";

import type { RouteState } from "./connections-model";

export function KeyBadge({ kind }: { kind: "personal" | "shared" }) {
  const t = useT(adminConnections);
  return kind === "personal" ? <Badge tone="core">{t("keyPersonal")}</Badge> : <Badge>{t("keyShared")}</Badge>;
}

export function ServerCell({ slug, profile }: { slug: string; profile?: string }) {
  return (
    <span className="flex min-w-0 items-center gap-2.5">
      <ServiceLogo slug={slug} name={slug} variant="catalog" size="sm" />
      <span className="truncate font-mono text-[13px] text-fg">{slug}</span>
      {profile && profile !== "default" ? <span className="truncate font-mono text-xs text-fg-muted">{profile}</span> : null}
    </span>
  );
}

const TONE_TEXT = { ok: "text-fg-muted", warn: "text-warn", cut: "text-cut" } as const;

/** The route glyph shows where a call with this key would stop; the words say why. */
export function StateCell({ route, keyKind }: { route: RouteState; keyKind: "personal" | "shared" }) {
  return (
    <span className="flex min-w-0 items-center gap-2">
      <RouteLine size="mini" keyKind={keyKind} result={route.result} cut={route.cut} label={route.label} />
      <span className={cn("truncate text-xs font-medium", TONE_TEXT[route.tone])}>{route.label}</span>
    </span>
  );
}

export const lastUsedText = (iso: string | null): string => (iso ? formatDateTime(iso) : translate(getLocale(), adminConnections, "never"));

/** Expiry in words; a key without a date renews itself on OAuth servers and does not expire otherwise. */
export function ExpiryText({ iso, authKind, gone = false }: { iso: string | null; authKind?: string; gone?: boolean }) {
  const t = useT(adminConnections);
  const text = iso ? formatDateTime(iso) : authKind === "oauth" ? t("expiryRenews") : t("expiryNone");
  return <span className={cn("block text-xs leading-4", gone ? "text-cut" : "text-fg-muted")}>{text}</span>;
}

export interface RowCheckboxProps {
  checked: boolean;
  indeterminate?: boolean;
  disabled?: boolean;
  onChange: () => void;
  label: string;
}

/** Native checkbox under a drawn box: keeps Space, focus and the accessible name; the label gives a 32px hit area. */
export function RowCheckbox({ checked, indeterminate = false, disabled, onChange, label }: RowCheckboxProps): ReactNode {
  return (
    <label className={cn("group -m-1.5 grid size-8 place-items-center", disabled ? "cursor-not-allowed opacity-45" : "cursor-pointer")}>
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={onChange}
        aria-label={label}
        ref={(el) => {
          if (el) el.indeterminate = indeterminate;
        }}
        className="peer sr-only"
      />
      <span
        aria-hidden
        className={cn(
          "grid size-4 place-items-center rounded-[4px] border transition-[background-color,border-color] duration-150 ease-switch group-hover:border-fg-muted peer-focus-visible:ring-2 peer-focus-visible:ring-signal-fg peer-focus-visible:ring-offset-2 peer-focus-visible:ring-offset-ink motion-reduce:transition-none",
          checked || indeterminate ? "border-signal bg-signal text-white" : "border-fg-subtle bg-ink/70",
        )}
      >
        {indeterminate ? <Minus className="size-3" strokeWidth={3} /> : checked ? <Check className="size-3" strokeWidth={3} /> : null}
      </span>
    </label>
  );
}
