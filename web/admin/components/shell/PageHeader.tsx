// Ported from design/v5/src/components/shell/PageHeader.tsx on 2026-10-31.
import type { ReactNode } from "react";

import { cn } from "../../lib/utils";

export interface PageHeaderProps {
  title: ReactNode;
  /** One or two sentences: what the screen is for, in product words. */
  description?: ReactNode;
  /** Breadcrumb or back link above the title. */
  eyebrow?: ReactNode;
  /** Buttons on the right (wrap under the title on mobile). */
  actions?: ReactNode;
  className?: string;
}

/** Title block for every screen that does not open with the switchboard. */
export function PageHeader({ title, description, eyebrow, actions, className }: PageHeaderProps) {
  return (
    <header className={cn("flex flex-wrap items-end justify-between gap-x-6 gap-y-4", className)}>
      <div className="min-w-0 max-w-2xl">
        {eyebrow ? <div className="mb-2 text-sm text-fg-muted">{eyebrow}</div> : null}
        <h1 className="text-2xl font-semibold tracking-[-0.03em] text-balance text-fg sm:text-[28px] sm:leading-9">{title}</h1>
        {description ? <p className="mt-2 text-sm leading-6 text-pretty text-fg-muted">{description}</p> : null}
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </header>
  );
}
