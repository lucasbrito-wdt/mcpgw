// Ported from design/v5/src/components/ui/badge.tsx on 2026-10-31.
"use client";

import {
  BookOpen,
  KeyRound,
  LockOpen,
  PencilLine,
  ShieldAlert,
  TimerOff,
  Trash2,
  Unplug,
  Users,
  type LucideIcon,
} from "lucide-react";
import type { ReactNode } from "react";

import { useT } from "../../lib/i18n";
import { labels } from "../../lib/i18n/messages/labels";
import { cn } from "../../lib/utils";

export type BadgeTone = "neutral" | "signal" | "core" | "ok" | "warn" | "cut";

const TONE: Record<BadgeTone, string> = {
  neutral: "bg-white/6 text-fg-muted ring-line-strong",
  signal: "bg-signal-wash text-signal-fg ring-signal/40",
  core: "bg-core/15 text-spark ring-core/50",
  ok: "bg-ok-wash text-ok ring-ok/30",
  warn: "bg-warn-wash text-warn ring-warn/30",
  cut: "bg-cut-wash text-cut ring-cut/35",
};

export interface BadgeProps {
  tone?: BadgeTone;
  icon?: LucideIcon;
  children: ReactNode;
  className?: string;
}

/** Small status label (Airbyte connector pill shape: 12px, radius 4). */
export function Badge({ tone = "neutral", icon: Icon, children, className }: BadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex h-5 shrink-0 items-center gap-1 whitespace-nowrap rounded-[4px] px-1.5 text-xs font-medium ring-1 ring-inset",
        TONE[tone],
        className,
      )}
    >
      {Icon ? <Icon className="size-3" aria-hidden /> : null}
      {children}
    </span>
  );
}

/** Every named pill in the product, so the same state always reads the same way. */
export type PillKind =
  | "personal"
  | "shared"
  | "none"
  | "open"
  | "revoked"
  | "expired"
  | "scopes"
  | "read"
  | "write"
  | "destroy";

const PILLS: Record<PillKind, { label: keyof typeof labels.en; tone: BadgeTone; icon: LucideIcon }> = {
  personal: { label: "key_personal", tone: "core", icon: KeyRound },
  shared: { label: "key_shared", tone: "neutral", icon: Users },
  none: { label: "key_none", tone: "neutral", icon: Unplug },
  open: { label: "key_open", tone: "ok", icon: LockOpen },
  revoked: { label: "problem_revoked", tone: "cut", icon: ShieldAlert },
  expired: { label: "problem_expired", tone: "warn", icon: TimerOff },
  scopes: { label: "problem_scopes", tone: "warn", icon: ShieldAlert },
  read: { label: "level_read", tone: "neutral", icon: BookOpen },
  write: { label: "level_write", tone: "signal", icon: PencilLine },
  destroy: { label: "level_destructive", tone: "cut", icon: Trash2 },
};

export interface PillProps {
  kind: PillKind;
  /** Hide the icon in dense rows. */
  bare?: boolean;
  className?: string;
}

/** Preset pill: key in use (personal, shared, none, open), connection problem, or tool level. */
export function Pill({ kind, bare = false, className }: PillProps) {
  const t = useT(labels);
  const pill = PILLS[kind];
  return (
    <Badge tone={pill.tone} icon={bare ? undefined : pill.icon} className={className}>
      {t(pill.label)}
    </Badge>
  );
}
