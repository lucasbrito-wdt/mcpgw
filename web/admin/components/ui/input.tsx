// Ported from design/v5/src/components/ui/input.tsx on 2026-10-31.
"use client";

import { Search, X } from "lucide-react";
import { useEffect, useRef, useState, type ComponentProps } from "react";

import { useT } from "../../lib/i18n";
import { ui } from "../../lib/i18n/messages/ui";
import { cn } from "../../lib/utils";

/** Airbyte control: 1px #707089 border, radius 6, signal border on focus, cut border when aria-invalid. */
const CONTROL =
  "h-9 w-full min-w-0 rounded-control border border-fg-subtle bg-ink/70 px-3 text-sm text-fg outline-none transition-[border-color,box-shadow] duration-150 ease-switch placeholder:text-fg-muted/80 hover:border-fg-muted focus-visible:border-signal focus-visible:ring-3 focus-visible:ring-signal/30 disabled:cursor-not-allowed disabled:opacity-45 aria-invalid:border-cut aria-invalid:ring-cut/25";

/** Text input with the Airbyte control look. Pass aria-invalid for the error state. */
export function Input({ className, type = "text", ...props }: ComponentProps<"input">) {
  return <input type={type} data-slot="input" className={cn(CONTROL, className)} {...props} />;
}

function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName);
}

export interface SearchFieldProps {
  value: string;
  onValueChange: (value: string) => void;
  /** Visible placeholder and accessible label. */
  label: string;
  placeholder?: string;
  /** "/" anywhere on the page focuses this field. One per page. */
  shortcut?: boolean;
  className?: string;
}

/** Search box with icon, clear button and the "/" shortcut hint. */
export function SearchField({ value, onValueChange, label, placeholder, shortcut = true, className }: SearchFieldProps) {
  const t = useT(ui);
  const inputRef = useRef<HTMLInputElement>(null);
  const [isFocused, setIsFocused] = useState(false);

  useEffect(() => {
    if (!shortcut) return;
    function onKeyDown(event: KeyboardEvent) {
      if (event.key !== "/" || event.metaKey || event.ctrlKey || event.altKey || isTypingTarget(event.target)) return;
      event.preventDefault();
      inputRef.current?.focus();
      inputRef.current?.select();
    }
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [shortcut]);

  return (
    <label className={cn("relative block", className)}>
      <span className="sr-only">{label}</span>
      <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-fg-muted" aria-hidden />
      <input
        ref={inputRef}
        type="text"
        inputMode="search"
        enterKeyHint="search"
        autoComplete="off"
        spellCheck={false}
        value={value}
        placeholder={placeholder ?? label}
        onChange={(event) => onValueChange(event.target.value)}
        onFocus={() => setIsFocused(true)}
        onBlur={() => setIsFocused(false)}
        onKeyDown={(event) => {
          if (event.key === "Escape" && value) {
            event.preventDefault();
            onValueChange("");
          }
        }}
        className={cn(CONTROL, "pr-9 pl-9")}
      />
      {value ? (
        <button
          type="button"
          onClick={() => {
            onValueChange("");
            inputRef.current?.focus();
          }}
          className="absolute top-1/2 right-1.5 grid size-6 -translate-y-1/2 cursor-pointer place-items-center rounded-[4px] text-fg-muted outline-none hover:bg-white/8 hover:text-fg focus-visible:ring-2 focus-visible:ring-signal-fg"
          aria-label={t("clearSearch")}
        >
          <X className="size-3.5" aria-hidden />
        </button>
      ) : shortcut && !isFocused ? (
        <kbd
          className="pointer-events-none absolute top-1/2 right-2 grid h-5 min-w-5 -translate-y-1/2 place-items-center rounded-[4px] border border-line-strong px-1 font-mono text-[11px] text-fg-muted"
          aria-hidden
        >
          /
        </kbd>
      ) : null}
    </label>
  );
}
