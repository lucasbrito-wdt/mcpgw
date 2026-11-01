// Ported from design/v5/src/components/admin-servers/method-cards.tsx on 2026-10-31.
"use client";

import { KeyRound, LockOpen, ShieldCheck } from "lucide-react";

import type { AuthKind } from "../../lib/admin-types";
import { useT } from "../../lib/i18n";
import { serversWizard } from "../../lib/i18n/messages/serversWizard";

import { OptionCard } from "./option-card";

const METHODS: { value: AuthKind; icon: typeof KeyRound }[] = [
  { value: "none", icon: LockOpen },
  { value: "oauth", icon: ShieldCheck },
  { value: "header", icon: KeyRound },
];

export interface MethodCardsProps {
  value: AuthKind;
  onValueChange: (method: AuthKind) => void;
  disabled?: boolean;
}

/** How the upstream authenticates: the three auth_kind values as option cards. */
export function MethodCards({ value, onValueChange, disabled }: MethodCardsProps) {
  const t = useT(serversWizard);
  return (
    <fieldset className="min-w-0" disabled={disabled}>
      <legend className="mb-2 text-[13px] font-semibold text-fg">{t("methodLegend")}</legend>
      <div className="grid gap-2 sm:grid-cols-3">
        {METHODS.map((method) => (
          <OptionCard
            key={method.value}
            name="auth-method"
            value={method.value}
            checked={value === method.value}
            onSelect={(next) => onValueChange(next as AuthKind)}
            title={t(`method_${method.value}_title`)}
            description={t(`method_${method.value}_desc`)}
            icon={method.icon}
          />
        ))}
      </div>
    </fieldset>
  );
}
