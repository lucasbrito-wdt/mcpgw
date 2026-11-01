// Ported from design/v5/src/components/portal/client-setup.tsx on 2026-10-31.
"use client";

import { ClipboardPaste, MousePointerClick, SquareTerminal, type LucideIcon } from "lucide-react";
import Link from "next/link";
import type { ReactNode } from "react";

import { ServiceLogo } from "../brand/ServiceLogo";
import { buttonVariants } from "../ui/button-variants";
import { CopyButton } from "../ui/copy-button";
import { useT } from "../../lib/i18n";
import { portalAgents } from "../../lib/i18n/messages/portalAgents";
import { portalCommon } from "../../lib/i18n/messages/portalCommon";
import { cn } from "../../lib/utils";
import type { AgentId } from "../../lib/types";

import { CodeBlock } from "./code-block";
import { agentName, METHOD_LABEL_KEY, missingMethodNote, type ClientSetup as ClientSetupData, type InstallMethod } from "./clients";

const ICON: Record<InstallMethod, LucideIcon> = { url: ClipboardPaste, oneclick: MousePointerClick, cli: SquareTerminal };

function MethodRow({ method, available, children }: { method: InstallMethod; available: boolean; children: ReactNode }) {
  const t = useT(portalAgents);
  const Icon = ICON[method];
  return (
    <li className="grid gap-3 px-4 py-4 sm:grid-cols-[13rem_minmax(0,1fr)] sm:items-center sm:gap-6">
      <span className={cn("flex items-center gap-2.5 text-[13px] font-semibold", available ? "text-fg" : "text-fg-muted")}>
        <span className={cn("grid size-7 shrink-0 place-items-center rounded-control", available ? "bg-signal-wash text-signal-fg" : "bg-white/4")} aria-hidden>
          <Icon className="size-4" />
        </span>
        {t(METHOD_LABEL_KEY[method])}
      </span>
      <div className="min-w-0">{children}</div>
    </li>
  );
}

function Missing({ children }: { children: ReactNode }) {
  return <p className="text-[13px] leading-5 text-fg-muted">{children}</p>;
}

export interface ClientSetupProps {
  id: AgentId;
  client: ClientSetupData;
  mcpUrl: string;
  panelId: string;
  tabId: string;
}

/** How to point one client at mcpgw: the three install methods (unsupported ones say why) and its config file. */
export function ClientSetup({ id, client, mcpUrl, panelId, tabId }: ClientSetupProps) {
  const t = useT(portalAgents);
  const tCommon = useT(portalCommon);
  const name = agentName(id);

  return (
    <div id={panelId} role="tabpanel" aria-labelledby={tabId} className="flex flex-col gap-5">
      <div className="flex items-center gap-3">
        <ServiceLogo slug={id} name={name} variant="tile" size="md" />
        <div className="min-w-0">
          <h3 className="text-[15px] font-semibold text-fg">{t("installIn", { name })}</h3>
          <p className="text-[13px] text-fg-muted">{t("anyPath")}</p>
        </div>
      </div>

      <ul className="divide-y divide-line rounded-card border border-line bg-panel/60">
        <MethodRow method="url" available={Boolean(client.paste)}>
          {client.paste ? (
            <div className="flex flex-wrap items-center justify-between gap-3">
              <p className="min-w-0 text-[13px] leading-5 text-fg-muted">
                {t("pasteBefore")} <span className="text-fg">{client.paste}</span>, {t("pasteAfter")} <code className="font-mono text-xs break-all text-fg">{mcpUrl}</code>.
              </p>
              <CopyButton value={mcpUrl} label={t("copyUrlLabel", { name })} text={t("copyUrl")} size="sm" />
            </div>
          ) : (
            <Missing>{missingMethodNote(client, "url", name, t)}</Missing>
          )}
        </MethodRow>

        <MethodRow method="oneclick" available={Boolean(client.oneClick)}>
          {client.oneClick ? (
            <div className="flex flex-wrap items-center justify-between gap-3">
              <p className="min-w-0 text-[13px] leading-5 text-fg-muted">{t("oneclickBody", { name })}</p>
              <a href={client.oneClick.href} className={buttonVariants({ variant: "primary", size: "md" })}>
                {client.oneClick.label}
              </a>
            </div>
          ) : (
            <Missing>{missingMethodNote(client, "oneclick", name, t)}</Missing>
          )}
        </MethodRow>

        <MethodRow method="cli" available={Boolean(client.cli)}>
          {client.cli ? (
            <CodeBlock code={client.cli} copyLabel={t("copyCommandLabel", { name })} caption={t("terminal")} shell />
          ) : (
            <Missing>{missingMethodNote(client, "cli", name, t)}</Missing>
          )}
        </MethodRow>
      </ul>

      <section aria-labelledby={`${panelId}-file`} className="flex flex-col gap-2">
        <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
          <h4 id={`${panelId}-file`} className="text-[13px] font-semibold text-fg">
            {t("editFile")}
          </h4>
          <span className="text-xs text-fg-muted">{client.file.language}</span>
        </div>
        <CodeBlock code={client.file.body} copyLabel={t("copyConfigLabel", { name })} caption={client.file.path} />
      </section>

      <ol className="grid gap-3 sm:grid-cols-3" aria-label={t("afterLabel")}>
        {[
          t("after1", { name }),
          t("after2"),
          null,
        ].map((text, index) => (
          <li key={index} className="flex gap-3 rounded-card border border-line px-3 py-3">
            <span className="grid size-5 shrink-0 place-items-center rounded-full bg-white/6 font-mono text-[11px] text-fg-muted" aria-hidden>
              {index + 1}
            </span>
            <p className="text-[13px] leading-5 text-fg-muted">
              {text ?? (
                <>
                  {t("after3")}{" "}
                  <Link href="/portal/" className="focus-ring rounded-[4px] text-signal-fg hover:underline">
                    {tCommon("viewServers")}
                  </Link>
                </>
              )}
            </p>
          </li>
        ))}
      </ol>
    </div>
  );
}
