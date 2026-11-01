"use client";

import { ConnectionsScreen } from "../../../components/admin-connections/connections-screen";
import { AppShell } from "../../../components/shell/AppShell";
import { PageHeader } from "../../../components/shell/PageHeader";
import { useT } from "../../../lib/i18n";
import { adminConnections } from "../../../lib/i18n/messages/adminConnections";

export default function AdminConnectionsPage() {
  const t = useT(adminConnections);
  return (
    <AppShell surface="admin">
      <title>{t("pageTitle")}</title>
      <div className="flex flex-col gap-6">
        <PageHeader title={t("title")} description={t("description")} />
        <ConnectionsScreen />
      </div>
    </AppShell>
  );
}
