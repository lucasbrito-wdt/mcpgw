"use client";

import { LOCALES, LOCALE_NAMES, useLocale, useT, type Locale } from "../../lib/i18n";
import { shell } from "../../lib/i18n/messages/shell";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";

const SHORT_LABEL: Record<Locale, string> = { en: "EN", "pt-BR": "PT-BR" };

/** Language toggle: one pressed button per locale. Each button is named by the language's own name. */
export function LocaleSwitcher({ className }: { className?: string }) {
  const { locale, setLocale } = useLocale();
  const t = useT(shell);
  return (
    <div role="group" aria-label={t("language")} className={cn("inline-flex items-center gap-0.5 rounded-control border border-line p-0.5", className)}>
      {LOCALES.map((code) => (
        <Button
          key={code}
          variant="ghost"
          size="sm"
          lang={code}
          aria-label={LOCALE_NAMES[code]}
          title={LOCALE_NAMES[code]}
          aria-pressed={code === locale}
          onClick={() => setLocale(code)}
          className={cn("h-6 px-2 text-[11px]", code === locale && "bg-signal-wash text-signal-fg hover:bg-signal-wash hover:text-signal-fg")}
        >
          {SHORT_LABEL[code]}
        </Button>
      ))}
    </div>
  );
}
