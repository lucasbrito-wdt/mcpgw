"use client";

import { ArrowLeft } from "lucide-react";
import Link from "next/link";
import { useState } from "react";

import { authKindLabel, serverKeyKind, useLoad, type Overview, type ServerDetail as ServerDetailData } from "../../lib/admin-types";
import { ApiError } from "../../lib/api";
import { useT } from "../../lib/i18n";
import { servers } from "../../lib/i18n/messages/servers";
import type { Me } from "../../lib/types";
import { PageHeader } from "../shell/PageHeader";
import { RouteLine } from "../signature/RouteLine";
import { AppShell } from "../shell/AppShell";
import { Badge } from "../ui/badge";
import { EmptyState } from "../ui/empty-state";
import { SegmentedTabs } from "../ui/segmented-tabs";

import { DetailActivity } from "./detail-activity";
import { DetailConnection } from "./detail-connection";
import { DetailCredentials } from "./detail-credentials";
import { DetailTools } from "./detail-tools";
import { LoadError, RowsSkeleton } from "./load-states";
import { StatusBadge } from "./server-cells";

type Section = "tools" | "connection" | "credentials" | "activity";

const SECTIONS: Section[] = ["tools", "connection", "credentials", "activity"];

function BackLink() {
  const t = useT(servers);
  return (
    <Link href="/admin/servers/" className="focus-ring inline-flex items-center gap-1.5 rounded-control hover:text-fg">
      <ArrowLeft className="size-4" aria-hidden />
      {t("back")}
    </Link>
  );
}

function hostOf(server: ServerDetailData): string {
  if (server.docker_image) return server.docker_image;
  try {
    return new URL(server.url).host;
  } catch {
    return server.url;
  }
}

function Body({ server, me, reload }: { server: ServerDetailData; me: string; reload: () => void }) {
  const t = useT(servers);
  const [section, setSection] = useState<Section>("tools");
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        eyebrow={<BackLink />}
        title={<span className="font-mono">{server.slug}</span>}
        description={server.category ? t("categoryDesc", { category: server.category }) : t("noCategoryDesc")}
        actions={
          <>
            <Badge>{server.credential_env ? t("perUserToken") : authKindLabel(server.auth_kind)}</Badge>
            <StatusBadge status={server.status} />
          </>
        }
      />
      <RouteLine
        size="hero"
        server={{ slug: server.slug, name: server.slug, host: hostOf(server) }}
        current={serverKeyKind(server)}
        sharedAccount={server.has_shared_connection ? t("sharedConnection") : undefined}
        caller={me}
      />
      <SegmentedTabs label={t("sectionsLabel")} controls="detail-panel" value={section} onValueChange={setSection} items={SECTIONS.map((value) => ({ value, label: t(`section_${value}`), count: value === "tools" ? server.active_tools + server.pending_tools : undefined }))} />
      <div id="detail-panel" role="tabpanel">
        {section === "tools" ? <DetailTools slug={server.slug} /> : null}
        {section === "connection" ? <DetailConnection server={server} onChanged={reload} /> : null}
        {section === "credentials" ? <DetailCredentials server={server} onChanged={reload} /> : null}
        {section === "activity" ? <DetailActivity slug={server.slug} /> : null}
      </div>
    </div>
  );
}

/** Server detail screen: header, the key route, and the four sections. `slug` comes from `?slug=`; undefined = not read yet. */
export function ServerDetailScreen({ slug }: { slug: string | null | undefined }) {
  const t = useT(servers);
  const server = useLoad<ServerDetailData>(slug ? `/servers/${encodeURIComponent(slug)}` : null);
  const me = useLoad<Me>("/me");
  const overview = useLoad<Overview>("/overview");
  const missing = slug === null || (server.error instanceof ApiError && server.error.status === 404);

  return (
    <AppShell surface="admin" reviewCount={overview.data?.pending_approvals}>
      <title>{slug ? t("titleWithSlug", { slug }) : t("titleFallback")}</title>
      {missing ? (
        <EmptyState title={t("notFoundTitle")} action={<Link href="/admin/servers/" className="focus-ring rounded-control text-sm text-signal-fg underline underline-offset-4">{t("backToList")}</Link>}>
          {t("notFoundBody")}
        </EmptyState>
      ) : null}
      {!missing && server.error ? <LoadError error={server.error} onRetry={server.reload} /> : null}
      {!missing && !server.error && !server.data ? <RowsSkeleton rows={3} label={t("loadingServer")} /> : null}
      {server.data ? <Body key={server.data.slug} server={server.data} me={me.data?.email ?? t("you")} reload={server.reload} /> : null}
    </AppShell>
  );
}
