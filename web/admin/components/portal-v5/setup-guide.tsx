// Ported from design/v5/src/components/portal/setup-guide.tsx on 2026-10-31.
// The contract gives the help link and the format hint only (no header name, permissions or expiry).
"use client";

import { ExternalLink } from "lucide-react";
import { useId, type ReactNode } from "react";

import { useT } from "../../lib/i18n";
import { portalConnect } from "../../lib/i18n/messages/portalConnect";
import { credentialEnvNote, credentialWordsOf, isHttpsUrl } from "../../lib/portal-types";
import { cn } from "../../lib/utils";

export interface SetupGuideProps {
  serverName: string;
  helpUrl: string | null;
  formatHint: string | null;
  /** stdio server: the token goes to this process variable and cannot be tested before saving. */
  credentialEnv?: string | null;
  className?: string;
}

function GuideStep({ n, children }: { n: number; children: ReactNode }) {
  return (
    <li className="grid grid-cols-[1.25rem_minmax(0,1fr)] gap-2 text-fg-muted">
      <span className="grid size-5 place-items-center rounded-full bg-signal-wash font-mono text-[11px] text-signal-fg" aria-hidden>
        {n}
      </span>
      <span className="min-w-0 text-pretty [overflow-wrap:anywhere]">{children}</span>
    </li>
  );
}

/**
 * The Airbyte "Setup Guide" beside the form: how to generate the token on the provider, in order.
 * Steps are numbered because they are a real sequence (you cannot paste before generating).
 */
export function SetupGuide({ serverName, helpUrl, formatHint, credentialEnv, className }: SetupGuideProps) {
  const t = useT(portalConnect);
  const titleId = useId();
  const link = isHttpsUrl(helpUrl) ? helpUrl : null;
  const words = credentialWordsOf(credentialEnv);
  return (
    <aside aria-labelledby={titleId} className={cn("flex flex-col gap-4 rounded-card bg-guide p-4 text-[13px] leading-5", className)}>
      <div className="flex items-center justify-between gap-3">
        <h3 id={titleId} className="text-sm font-semibold text-fg">
          {t("setupGuide")}
        </h3>
        {link ? (
          <a href={link} target="_blank" rel="noopener noreferrer" className="focus-ring inline-flex items-center gap-1 rounded-[4px] text-xs text-signal-fg hover:underline">
            {t("guideDocs")}
            <ExternalLink className="size-3" aria-hidden />
            <span className="sr-only">{t("opensNewTab")}</span>
          </a>
        ) : null}
      </div>
      {formatHint ? (
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 rounded-card bg-black/30 p-3 text-xs">
          <dt className="text-fg-muted">{t("guideFormat")}</dt>
          <dd className="min-w-0 font-mono break-all text-fg">{t("guideStartsWith")} {formatHint}</dd>
        </dl>
      ) : null}
      <ol className="flex flex-col gap-3">
        <GuideStep n={1}>{t(`guideStep1_${words.kind}`, { name: serverName })}</GuideStep>
        <GuideStep n={2}>
          {t(`guideCopy_${words.kind}`)}
          {formatHint ? <> ({t("guideStartsWith")} <code className="font-mono text-fg">{formatHint}</code>)</> : null} {t("guidePaste")}
          {words.kind === "token" ? ` ${t("guideShownOnce")}` : null}
        </GuideStep>
        {credentialEnv ? (
          <GuideStep n={3}>
            {credentialEnvNote(credentialEnv)} {t(`guideStep3Env_${words.kind}`)}
          </GuideStep>
        ) : (
          <GuideStep n={3}>{t("guideStep3Test", { name: serverName })}</GuideStep>
        )}
      </ol>
    </aside>
  );
}
