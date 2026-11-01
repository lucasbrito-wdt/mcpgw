// Ported from design/v5/src/components/admin-servers/setup-guide.tsx on 2026-10-31.
"use client";

import { ExternalLink } from "lucide-react";
import type { ReactNode } from "react";

import type { AuthKind } from "../../lib/admin-types";
import { useT } from "../../lib/i18n";
import { serversWizard } from "../../lib/i18n/messages/serversWizard";

export interface SetupGuideProps {
  method: AuthKind;
  /** Docker (stdio) server: the token goes to a process variable, not to a header. */
  stdio?: boolean;
  credentialEnv?: string;
  serverName: string;
  helpUrl: string;
  headerName: string;
  prefix: string;
}

function Step({ children }: { children: ReactNode }) {
  return <li className="pl-1 marker:font-mono marker:text-fg-muted">{children}</li>;
}

function Code({ children }: { children: ReactNode }) {
  return <code className="rounded-[4px] bg-ink/70 px-1 py-0.5 font-mono text-[12px] text-fg">{children}</code>;
}

/** Only an https link reaches an href; anything else is shown as no link (the form validates it too). */
const safeHelpUrl = (url: string): string => (url.trim().startsWith("https://") ? url.trim() : "");

/** The Airbyte "Setup Guide" box: short ordered steps for the method picked beside it. */
export function SetupGuide({ method, stdio, credentialEnv, serverName, helpUrl, headerName, prefix }: SetupGuideProps) {
  const t = useT(serversWizard);
  const link = safeHelpUrl(helpUrl);
  return (
    <aside aria-labelledby="setup-guide-title" className="flex h-fit flex-col gap-3 rounded-card bg-guide p-4 text-[13px] leading-6 text-fg-muted lg:sticky lg:top-0">
      <h3 id="setup-guide-title" className="text-sm font-semibold text-fg">
        {t("guideTitle")}
      </h3>
      {stdio ? (
        credentialEnv ? (
          <ol className="flex list-decimal flex-col gap-2 pl-4">
            <Step>
              {t("guideStdioGenerate", { server: serverName })}
              {link ? (
                <>
                  {": "}
                  <a href={link} target="_blank" rel="noreferrer" className="focus-ring inline-flex items-center gap-1 rounded-[3px] text-signal-fg underline underline-offset-4 hover:text-fg">
                    {t("guideOpenTokens")}
                    <ExternalLink className="size-3" aria-hidden />
                  </a>
                </>
              ) : null}
              .
            </Step>
            <Step>
              {t("guideStdioPaste_a")} <Code>{credentialEnv}</Code> {t("guideStdioPaste_b")}
            </Step>
            <Step>{t("guideStdioStart")}</Step>
          </ol>
        ) : (
          <p>{t("guideStdioNoCred")}</p>
        )
      ) : null}
      {!stdio && method === "header" ? (
        <>
          <ol className="flex list-decimal flex-col gap-2 pl-4">
            <Step>
              {t("guideHeaderGenerate", { server: serverName })}
              {link ? (
                <>
                  :{" "}
                  <span className="whitespace-nowrap">
                    <a href={link} target="_blank" rel="noreferrer" className="focus-ring inline-flex items-center gap-1 rounded-[3px] text-signal-fg underline underline-offset-4 hover:text-fg">
                      {t("guideOpenTokens")}
                      <ExternalLink className="size-3" aria-hidden />
                    </a>
                    .
                  </span>
                </>
              ) : (
                "."
              )}
            </Step>
            <Step>
              {t("guideHeaderWhich")} <Code>{headerName || "Authorization"}</Code>
              {prefix.trim() ? (
                <>
                  {" "}
                  {t("guideWithPrefix")} <Code>{prefix.trim()}</Code>
                </>
              ) : null}
              .
            </Step>
            <Step>{t("guideHeaderShared")}</Step>
          </ol>
          <p className="border-t border-line pt-3 text-xs">
            {t("guideHeaderNote")}
          </p>
        </>
      ) : null}
      {!stdio && method === "oauth" ? (
        <>
          <ol className="flex list-decimal flex-col gap-2 pl-4">
            <Step>{t("guideOauthDcr")}</Step>
            <Step>
              {t("guideOauthApp_a", { server: serverName })} <strong className="font-semibold text-fg">{t("guideOauthApp_strong")}</strong>.
            </Step>
            <Step>{t("guideOauthCopy")}</Step>
          </ol>
        </>
      ) : null}
      {!stdio && method === "none" ? (
        <p>{t("guideNone", { server: serverName })}</p>
      ) : null}
    </aside>
  );
}
