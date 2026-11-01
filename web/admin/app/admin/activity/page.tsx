"use client";

import { LogsScreen } from "../../../components/admin-logs/logs-screen";
import { AppShell } from "../../../components/shell/AppShell";
import { PageHeader } from "../../../components/shell/PageHeader";
import { useT } from "../../../lib/i18n";
import { adminLogs } from "../../../lib/i18n/messages/adminLogs";

export default function AdminActivityPage() {
  const t = useT(adminLogs);
  return (
    <AppShell surface="admin">
      <title>{t("pageTitle")}</title>
      <div className="flex flex-col gap-6">
        <PageHeader title={t("title")} description={t("description")} />
        <LogsScreen />
      </div>
    </AppShell>
  );
}
