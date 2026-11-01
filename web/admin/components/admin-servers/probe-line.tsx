import { Check, CircleAlert, ShieldAlert } from "lucide-react";

import { cn } from "../../lib/utils";

import type { ProbeMessage } from "./probe-format";

const STYLE = {
  ok: { icon: Check, className: "text-ok" },
  warn: { icon: ShieldAlert, className: "text-warn" },
  cut: { icon: CircleAlert, className: "text-cut" },
} as const;

/** Inline probe outcome. The text may come from the backend, so it renders as a text node only. */
export function ProbeLine({ message, className }: { message: ProbeMessage; className?: string }) {
  const { icon: Icon, className: tone } = STYLE[message.tone];
  return (
    <p role={message.tone === "cut" ? "alert" : "status"} className={cn("flex items-start gap-2 text-[13px]", tone, className)}>
      <Icon className="mt-0.5 size-4 shrink-0" aria-hidden />
      <span className="min-w-0 break-words">{message.text}</span>
    </p>
  );
}
