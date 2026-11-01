import type { Metadata } from "next";
import { Suspense } from "react";

import { ConnectScreen } from "../../../components/portal-v5/connect-screen";
import { AppShell } from "../../../components/shell/AppShell";
import { portalDetail } from "../../../lib/i18n/messages/portalDetail";

// Static export: the title is the default locale; the screen re-sets document.title for the active locale.
export const metadata: Metadata = { title: portalDetail.en.pageTitle };

export default function PortalConnectPage() {
  return (
    <AppShell surface="portal">
      <Suspense>
        <ConnectScreen />
      </Suspense>
    </AppShell>
  );
}
