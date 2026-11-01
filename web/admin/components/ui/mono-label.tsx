// Ported from design/v5/src/components/ui/mono-label.tsx on 2026-10-31.
import type { ReactNode } from "react";

import { cn } from "../../lib/utils";

export interface MonoLabelProps {
  as?: "span" | "p" | "h2" | "h3" | "dt";
  /** "strong" for the column heads of the switchboard, "quiet" for captions under nodes. */
  tone?: "strong" | "quiet";
  id?: string;
  children: ReactNode;
  className?: string;
}

/** The art's mono caps caption ("YOUR AGENTS", "KEY"). Only where the art would use one. */
export function MonoLabel({ as: Tag = "span", tone = "quiet", id, children, className }: MonoLabelProps) {
  return (
    <Tag id={id} className={cn("mono-label", tone === "strong" ? "text-fg" : "text-fg-muted", className)}>
      {children}
    </Tag>
  );
}
