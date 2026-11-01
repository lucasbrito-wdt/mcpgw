import type { Metadata } from "next";

import { AgentsScreen } from "../../../components/portal-v5/agents-screen";
import { AppShell } from "../../../components/shell/AppShell";
import { portalAgents } from "../../../lib/i18n/messages/portalAgents";

// Static export: the title is the default locale; the screen re-sets document.title for the active locale.
export const metadata: Metadata = { title: portalAgents.en.pageTitle };

export default function PortalAgentsPage() {
  return (
    <AppShell surface="portal">
      <AgentsScreen />
    </AppShell>
  );
}
