// Ported from design/v5/src/components/ui/toggle.tsx on 2026-10-31.
"use client";

import { Switch } from "@base-ui/react/switch";

import { cn } from "../../lib/utils";

import { useMounted } from "./motion-span";

export interface ToggleProps {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  /** Accessible name; pair with a visible label via aria-labelledby when there is one. */
  label: string;
  disabled?: boolean;
  className?: string;
}

const TRACK =
  "relative inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full border border-fg-subtle bg-ink p-0.5 outline-none transition-colors duration-150 ease-switch hover:border-fg-muted focus-visible:ring-2 focus-visible:ring-signal-fg focus-visible:ring-offset-2 focus-visible:ring-offset-ink data-checked:border-signal data-checked:bg-signal data-disabled:cursor-not-allowed data-disabled:opacity-45";
const THUMB =
  "block size-3.5 rounded-full bg-fg-muted shadow-sm transition-transform duration-150 ease-switch data-checked:translate-x-4 data-checked:bg-white motion-reduce:transition-none";

/** Airbyte on/off switch: dark track, signal when on. */
export function Toggle({ checked, onCheckedChange, label, disabled, className }: ToggleProps) {
  const mounted = useMounted();
  const rootClass = cn(TRACK, className);
  // Base UI's hidden input carries an inline style attribute, which the CSP blocks (plan gap G26),
  // so the server-rendered HTML gets an inert look-alike that the real switch replaces after mount.
  if (!mounted) {
    return (
      <span role="switch" aria-checked={checked} aria-label={label} aria-disabled={disabled} data-checked={checked ? "" : undefined} data-disabled={disabled ? "" : undefined} className={rootClass}>
        <span data-checked={checked ? "" : undefined} className={THUMB} />
      </span>
    );
  }
  return (
    <Switch.Root checked={checked} onCheckedChange={(next) => onCheckedChange(next)} disabled={disabled} aria-label={label} className={rootClass}>
      <Switch.Thumb className={THUMB} />
    </Switch.Root>
  );
}
