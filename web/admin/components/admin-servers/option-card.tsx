// Ported from design/v5/src/components/admin-servers/option-card.tsx on 2026-10-31.
import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

import { cn } from "../../lib/utils";

export interface OptionCardProps {
  /** Radio group name. */
  name: string;
  value: string;
  checked: boolean;
  onSelect: (value: string) => void;
  title: string;
  description?: ReactNode;
  icon?: LucideIcon;
  disabled?: boolean;
  className?: string;
}

/**
 * Selectable card backed by a real radio input: arrow keys move inside the group, the card shows
 * hover, focus-visible, checked and disabled. Used for auth methods, OAuth client and revoke reasons.
 */
export function OptionCard({ name, value, checked, onSelect, title, description, icon: Icon, disabled, className }: OptionCardProps) {
  return (
    <label
      className={cn(
        "group relative flex cursor-pointer items-start gap-3 rounded-card border border-line-strong bg-ink/50 p-3 transition-[border-color,background-color] duration-150 ease-switch hover:border-fg-subtle has-checked:border-signal has-checked:bg-signal-wash has-focus-visible:ring-2 has-focus-visible:ring-signal-fg has-disabled:cursor-not-allowed has-disabled:opacity-45 has-disabled:hover:border-line-strong motion-reduce:transition-none",
        className,
      )}
    >
      <input type="radio" name={name} value={value} checked={checked} disabled={disabled} onChange={() => onSelect(value)} className="peer sr-only" />
      <span
        aria-hidden
        className="mt-0.5 grid size-4 shrink-0 place-items-center rounded-full border border-fg-subtle transition-colors duration-150 ease-switch group-has-checked:border-signal-fg"
      >
        <span className="size-2 scale-0 rounded-full bg-signal-fg transition-transform duration-150 ease-switch group-has-checked:scale-100 motion-reduce:transition-none" />
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-2 text-[13px] font-semibold text-fg">
          {Icon ? <Icon className="size-4 shrink-0 text-fg-muted group-has-checked:text-signal-fg" aria-hidden /> : null}
          {title}
        </span>
        {description ? <span className="mt-1 block text-xs leading-5 text-fg-muted">{description}</span> : null}
      </span>
    </label>
  );
}
