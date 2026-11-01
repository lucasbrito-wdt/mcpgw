import type { Metadata } from "next";
import { Suspense } from "react";

import { ServersScreen } from "../../components/portal-v5/servers-screen";
import { AppShell } from "../../components/shell/AppShell";
import { portalServers } from "../../lib/i18n/messages/portalServers";

// Static export: the title is the default locale; the screen re-sets document.title for the active locale.
export const metadata: Metadata = { title: portalServers.en.pageTitle };

export default function PortalPage() {
  return (
    <AppShell surface="portal">
      <Suspense>
        <ServersScreen />
      </Suspense>
    </AppShell>
  );
}
