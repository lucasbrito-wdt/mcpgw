// Ported from design/v5/src/components/admin-servers/field.tsx on 2026-10-31.
"use client";

import { CircleAlert } from "lucide-react";
import type { ReactNode } from "react";

import { useT } from "../../lib/i18n";
import { servers } from "../../lib/i18n/messages/servers";
import { cn } from "../../lib/utils";

export interface FieldProps {
  /** Id of the control; hint and error get `${id}-hint` and `${id}-error`. */
  id: string;
  label: string;
  /** Shown after the label in muted text. */
  optional?: boolean;
  hint?: ReactNode;
  error?: ReactNode;
  children: ReactNode;
  className?: string;
}

/** The ids a control must list in aria-describedby for the hint and error under it. */
export function describedBy(id: string, parts: { hint?: unknown; error?: unknown }): string | undefined {
  const ids = [parts.hint ? `${id}-hint` : null, parts.error ? `${id}-error` : null].filter(Boolean);
  return ids.length ? ids.join(" ") : undefined;
}

/** Label, control, hint and error with the Airbyte rhythm (label 13/600, hint 12 muted, error 12 cut with icon). */
export function Field({ id, label, optional, hint, error, children, className }: FieldProps) {
  const t = useT(servers);
  return (
    <div className={cn("flex min-w-0 flex-col gap-1.5", className)}>
      <label htmlFor={id} className="text-[13px] font-semibold text-fg">
        {label}
        {optional ? <span className="ml-1.5 font-normal text-fg-muted">{t("optional")}</span> : null}
      </label>
      {children}
      {hint && !error ? (
        <p id={`${id}-hint`} className="text-xs leading-5 text-fg-muted">
          {hint}
        </p>
      ) : null}
      {error ? (
        <p id={`${id}-error`} role="alert" className="flex items-start gap-1.5 text-xs leading-5 text-cut">
          <CircleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden />
          <span>{error}</span>
        </p>
      ) : null}
    </div>
  );
}
