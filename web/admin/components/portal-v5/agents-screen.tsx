// Ported from design/v5/src/components/portal/agents-screen.tsx on 2026-10-31.
// No API call: the address is this page's origin + /mcp, and per-agent activity is not in the contract.
"use client";

import { ShieldCheck } from "lucide-react";
import { useEffect, useId, useState, useSyncExternalStore } from "react";

import { CopyButton } from "../ui/copy-button";
import { MonoLabel } from "../ui/mono-label";
import { SegmentedTabs, segmentedTabId } from "../ui/segmented-tabs";
import { useT } from "../../lib/i18n";
import { portalAgents } from "../../lib/i18n/messages/portalAgents";
import type { AgentId } from "../../lib/types";

import { AgentSwitchboard } from "./agent-switchboard";
import { ClientSetup } from "./client-setup";
import { AGENTS, clientSetups } from "./clients";

const TABS = AGENTS.map((agent) => ({ value: agent.id, label: agent.name }));
const subscribe = () => () => {};

/** `<origin>/mcp`, the gateway's own address; empty during SSR and hydration. */
function useMcpUrl(): string {
  return useSyncExternalStore(
    subscribe,
    () => `${window.location.origin}/mcp`,
    () => "",
  );
}

/** /portal/agents: one MCP address for every client and how to install it per client. */
export function AgentsScreen() {
  const t = useT(portalAgents);
  const mcpUrl = useMcpUrl();
  const [selected, setSelected] = useState<AgentId>("claude-code");
  const panelId = useId();
  const tabsId = useId();
  const setups = clientSetups(mcpUrl, t);

  // Static export: the page title is the default locale; this one follows the active locale.
  useEffect(() => {
    document.title = `${t("pageTitle")} | mcpgw`;
  }, [t]);

  return (
    <div className="flex flex-col gap-10">
      <section
        aria-labelledby="agents-title"
        className="@container relative isolate overflow-hidden rounded-panel border border-white/10 bg-linear-to-b from-black from-45% to-black/45 shadow-[inset_0_1px_0_rgb(255_255_255/0.06)]"
      >
        <div className="grid gap-8 p-5 @min-[40rem]:p-8 @min-[60rem]:grid-cols-[minmax(0,1fr)_minmax(0,24rem)] @min-[60rem]:items-center">
          <div className="flex min-w-0 flex-col gap-6">
            <div>
              <h1 id="agents-title" className="font-display text-display text-balance text-fg">
                {t("pageTitle")}
              </h1>
              <p className="mt-3 max-w-xl text-sm leading-6 text-pretty text-fg-muted">
                {t("intro")}
              </p>
            </div>

            <div className="flex flex-col gap-2">
              <MonoLabel as="p">{t("mcpAddress")}</MonoLabel>
              <div className="flex flex-col gap-2 rounded-card border border-line-strong bg-ink/70 p-2 pl-3 sm:flex-row sm:items-center">
                <code className="min-h-6 min-w-0 flex-1 truncate py-1 font-mono text-[15px] text-fg">{mcpUrl}</code>
                <CopyButton value={mcpUrl} label={t("copyAddressLabel")} text={t("copyAddress")} variant="primary" />
              </div>
              <p className="flex items-center gap-1.5 text-xs text-fg-muted">
                <ShieldCheck className="size-3.5 shrink-0 text-ok" aria-hidden />
                {t("transport")}
              </p>
            </div>
          </div>

          <AgentSwitchboard selected={selected} className="w-full max-w-md @min-[60rem]:ml-auto" />
        </div>
      </section>

      <section aria-labelledby="agents-install-title" className="flex flex-col gap-4">
        <div className="flex flex-col gap-3 @container">
          <h2 id="agents-install-title" className="text-[17px] font-semibold text-fg">
            {t("chooseClient")}
          </h2>
          <SegmentedTabs items={TABS} value={selected} onValueChange={setSelected} label={t("clientTabs")} controls={panelId} id={tabsId} />
        </div>
        <ClientSetup key={selected} id={selected} client={setups[selected]} mcpUrl={mcpUrl} panelId={panelId} tabId={segmentedTabId(tabsId, selected)} />
      </section>
    </div>
  );
}
