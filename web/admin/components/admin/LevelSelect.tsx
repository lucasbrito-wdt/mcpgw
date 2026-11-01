"use client";

import { levelLabel } from "../../lib/format";
import { useLocale } from "../../lib/i18n";
import type { AccessLevel } from "../../lib/types";
import { SegmentedTabs } from "../ui/segmented-tabs";

const LEVELS: readonly AccessLevel[] = ["read", "write", "destructive"];
const LEVELS_WITH_NONE: readonly AccessLevel[] = ["none", ...LEVELS];

export interface LevelSelectProps {
  value: AccessLevel;
  onValueChange: (level: AccessLevel) => void;
  /** Accessible name of the group, e.g. "Nível de create_issue". */
  label: string;
  /** Adds "Sem acesso" (ceilings: `none` removes the ceiling). Tool levels have no `none`. */
  allowNone?: boolean;
  disabled?: boolean;
  size?: "sm" | "md";
  className?: string;
}

/** Tool level or ceiling as a segmented control: Leitura, Escrita, Destrutivo (and Sem acesso on request). */
export function LevelSelect({ value, onValueChange, label, allowNone = false, disabled = false, size = "sm", className }: LevelSelectProps) {
  useLocale(); // re-render on a language switch: levelLabel reads the active locale
  const levels = allowNone ? LEVELS_WITH_NONE : LEVELS;
  return (
    <SegmentedTabs
      label={label}
      size={size}
      value={value}
      onValueChange={onValueChange}
      className={className}
      items={levels.map((level) => ({ value: level, label: levelLabel(level), disabled }))}
    />
  );
}
