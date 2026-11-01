"use client";

import { Plus } from "lucide-react";
import { useState } from "react";

import { AddServerWizard } from "../../../components/admin/AddServerWizard";
import { LoadError, RowsSkeleton } from "../../../components/admin-servers/load-states";
import { ServersScreen } from "../../../components/admin-servers/servers-screen";
import { AppShell } from "../../../components/shell/AppShell";
import { PageHeader } from "../../../components/shell/PageHeader";
import { Button } from "../../../components/ui/button";
import { useLoad, type Paged, type ServerSummary } from "../../../lib/admin-types";
import { useT } from "../../../lib/i18n";
import { adminServersPage } from "../../../lib/i18n/messages/adminServersPage";

export default function AdminServersPage() {
  const t = useT(adminServersPage);
  const servers = useLoad<Paged<ServerSummary>>("/servers?limit=200");
  const [wizardOpen, setWizardOpen] = useState(false);
  const openWizard = () => setWizardOpen(true);

  return (
    <AppShell surface="admin">
      <title>{t("pageTitle")}</title>
      <div className="flex flex-col gap-6">
        <PageHeader
          title={t("title")}
          description={t("description")}
          actions={
            <Button variant="primary" size="lg" onClick={openWizard}>
              <Plus aria-hidden />
              {t("add")}
            </Button>
          }
        />
        {servers.error ? <LoadError error={servers.error} onRetry={servers.reload} /> : null}
        {!servers.error && !servers.data ? <RowsSkeleton label={t("loading")} /> : null}
        {servers.data ? <ServersScreen servers={servers.data.items} onAdd={openWizard} /> : null}
      </div>
      <AddServerWizard open={wizardOpen} onOpenChange={setWizardOpen} onCreated={servers.reload} />
    </AppShell>
  );
}
