// Ported from design/v5/src/components/ui/empty-state.tsx on 2026-10-31.
import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

import { cn } from "../../lib/utils";

export interface EmptyStateProps {
  icon?: LucideIcon;
  title: string;
  /** What happened and how to get out of it. */
  children?: ReactNode;
  /** One button or link that clears the cause. */
  action?: ReactNode;
  className?: string;
}

/** Drawn empty state for lists, searches and filters. */
export function EmptyState({ icon: Icon, title, children, action, className }: EmptyStateProps) {
  return (
    <div className={cn("flex flex-col items-start gap-3 rounded-card border border-dashed border-line-strong bg-panel/40 p-6 sm:p-8", className)}>
      {Icon ? (
        <span className="grid size-9 place-items-center rounded-control bg-guide text-signal-fg" aria-hidden>
          <Icon className="size-4" />
        </span>
      ) : null}
      <div className="max-w-prose">
        <p className="text-sm font-semibold text-fg">{title}</p>
        {children ? <div className="mt-1 text-sm text-fg-muted">{children}</div> : null}
      </div>
      {action}
    </div>
  );
}
