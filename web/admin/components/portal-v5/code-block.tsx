// Ported from design/v5/src/components/portal/code-block.tsx on 2026-10-31.
import { CopyButton } from "../ui/copy-button";
import { cn } from "../../lib/utils";

export interface CodeBlockProps {
  code: string;
  /** Accessible name of the copy action ("Copy Claude Code command"). */
  copyLabel: string;
  /** Caption on the top bar: a file path or the language. */
  caption?: string;
  /** Shell commands get a "$" gutter that is not copied. */
  shell?: boolean;
  className?: string;
}

/** Code the person copies into a client: a command or a config file. Scrolls sideways, never wraps a command. */
export function CodeBlock({ code, copyLabel, caption, shell = false, className }: CodeBlockProps) {
  return (
    <div className={cn("min-w-0 overflow-hidden rounded-card border border-line bg-black/60", className)}>
      <div className="flex min-h-10 items-center justify-between gap-3 border-b border-line py-1.5 pr-1.5 pl-3">
        <span className="min-w-0 truncate font-mono text-xs text-fg-muted">{caption}</span>
        <CopyButton value={code} label={copyLabel} variant="ghost" />
      </div>
      <pre tabIndex={0} aria-label={caption} className="focus-ring overflow-x-auto p-3 font-mono text-[12.5px] leading-5 text-fg">
        <code>
          {shell ? (
            <span className="text-fg-muted select-none" aria-hidden>
              ${" "}
            </span>
          ) : null}
          {code}
        </code>
      </pre>
    </div>
  );
}
