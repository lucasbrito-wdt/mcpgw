// Ported from design/v5/src/components/portal/server-detail-screen.tsx on 2026-10-31.
// Hash states (#token, #oauth, #personal, #disconnect) are React state; data comes from the portal API.
"use client";

import { SearchX, TriangleAlert } from "lucide-react";
import Link from "next/link";
import { useEffect, useState } from "react";

import { TokenDialog } from "../portal/TokenDialog";
import { ToolsAccordion } from "../portal/ToolsAccordion";
import { Button } from "../ui/button";
import { buttonVariants } from "../ui/button-variants";
import { EmptyState } from "../ui/empty-state";
import { Skeleton } from "../ui/skeleton";
import { useT } from "../../lib/i18n";
import { portalCommon } from "../../lib/i18n/messages/portalCommon";
import { portalDetail } from "../../lib/i18n/messages/portalDetail";
import { DEFAULT_PROFILE, appName, authMethodsOf, type AuthMethod, type ConnectAudience } from "../../lib/portal-types";

import { ConnectMethodDialog } from "./connect-method-dialog";
import { ConnectOAuthDialog } from "./connect-oauth-dialog";
import { DisconnectDialog } from "./disconnect-dialog";
import { ProfileList } from "./profile-list";
import { ServerAbout } from "./server-about";
import { ServerHero, heroStateOf } from "./server-hero";
import { usePortalApp, usePortalMe } from "./use-portal-data";

type Dialog = "method" | "connect" | "disconnect" | null;

function DetailSkeleton() {
  const t = useT(portalDetail);
  return (
    <div className="flex flex-col gap-8" aria-busy="true" aria-label={t("loadingServer")}>
      <Skeleton className="h-96 rounded-panel" />
      <Skeleton className="h-64 rounded-card" />
    </div>
  );
}

/** Portal server detail (`/portal/connect/?slug=`): route hero with the key in use, connect and disconnect flows, tools and facts. */
export function ServerDetailScreen({ slug, initialProfile }: { slug: string | null; initialProfile?: string }) {
  const t = useT(portalDetail);
  const tCommon = useT(portalCommon);
  const { state, reload } = usePortalApp(slug);
  const me = usePortalMe();
  const [dialog, setDialog] = useState<Dialog>(null);
  const [audience, setAudience] = useState<ConnectAudience>("personal");
  const [profile, setProfile] = useState(DEFAULT_PROFILE);
  const [method, setMethod] = useState<AuthMethod | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  // Static export: the page title is the default locale; this one follows the active locale.
  useEffect(() => {
    document.title = `${t("pageTitle")} | mcpgw`;
  }, [t]);

  if (state.status === "loading") return <DetailSkeleton />;

  if (state.status === "missing") {
    return (
      <EmptyState
        icon={SearchX}
        title={t("notFoundTitle")}
        action={
          <Link href="/portal/" className={buttonVariants({ variant: "secondary" })}>
            {tCommon("viewServers")}
          </Link>
        }
      >
        {t("notFoundBody")}
      </EmptyState>
    );
  }

  if (state.status === "failed") {
    return (
      <EmptyState
        icon={TriangleAlert}
        title={t("loadFailedTitle")}
        action={
          <Button variant="secondary" onClick={reload}>
            {tCommon("retry")}
          </Button>
        }
      >
        {tCommon("loadFailedBody")}
      </EmptyState>
    );
  }

  const { app } = state;
  const name = appName(app);
  const isAdmin = me?.is_admin === true;
  const isShared = audience === "shared" && isAdmin;
  const heroState = heroStateOf(app);
  const profiles = app.profiles ?? [];
  const profileNames = profiles.map((item) => item.profile);
  // The name the agent asked for (?profile=) wins; else the one the hero shows; else default.
  const preferredProfile = initialProfile ?? profileNames[0] ?? DEFAULT_PROFILE;

  const methods = authMethodsOf(app);

  // One method goes straight to its dialog; two ask the viewer first.
  function openConnect(next: ConnectAudience, target = preferredProfile) {
    setAudience(next);
    setProfile(target);
    // A pasted token is per-user only: the shared connection of an oauth server stays OAuth.
    const allowed = next === "shared" && methods.includes("oauth") ? (["oauth"] as AuthMethod[]) : methods;
    if (allowed.length > 1) {
      setDialog("method");
      return;
    }
    setMethod(allowed[0] ?? null);
    setDialog("connect");
  }

  function chooseMethod(chosen: AuthMethod) {
    setMethod(chosen);
    setDialog("connect");
  }

  function openDisconnect(target = preferredProfile) {
    setProfile(target);
    setDialog("disconnect");
  }

  function connected(saved: string = DEFAULT_PROFILE) {
    setDialog(null);
    setNotice(
      isShared
        ? t("noticeSharedSwapped", { name })
        : saved === DEFAULT_PROFILE
          ? t("noticeConnected", { name })
          : t("noticeConnectedAs", { profile: saved }),
    );
    reload();
  }

  function disconnected() {
    setDialog(null);
    setNotice(
      profile !== DEFAULT_PROFILE
        ? t("noticeDisconnectedProfile", { profile, name })
        : app.shared_available
          ? t("noticeDisconnectedShared")
          : t("noticeDisconnectedNone", { name }),
    );
    reload();
  }

  return (
    <div className="flex flex-col gap-8">
      <ServerHero
        app={app}
        name={name}
        caller={me?.email ?? ""}
        isAdmin={isAdmin}
        notice={notice}
        onDismissNotice={() => setNotice(null)}
        onConnect={openConnect}
        onDisconnect={() => openDisconnect()}
      />

      {app.auth_kind !== "none" ? (
        <ProfileList name={name} profiles={profiles} onAdd={() => openConnect("personal", "")} onDisconnect={openDisconnect} />
      ) : null}

      <div className="grid grid-cols-[minmax(0,1fr)] items-start gap-8 lg:grid-cols-[minmax(0,1fr)_18rem] xl:grid-cols-[minmax(0,1fr)_20rem]">
        <ToolsAccordion
          serverName={name}
          tools={app.tools}
          needsConnection={heroState.kind === "none"}
          onConnect={app.auth_kind === "none" ? undefined : () => openConnect("personal")}
        />
        <ServerAbout app={app} isAdmin={isAdmin} />
      </div>

      {methods.length > 1 ? (
        <ConnectMethodDialog
          name={name}
          open={dialog === "method"}
          onOpenChange={(open) => setDialog(open ? "method" : null)}
          initial={profiles.find((item) => item.profile === profile)?.method}
          onChoose={chooseMethod}
        />
      ) : null}

      {methods.includes("token") && method === "token" ? (
        <TokenDialog
          slug={app.slug}
          name={name}
          open={dialog === "connect"}
          onOpenChange={(open) => setDialog(open ? "connect" : null)}
          shared={isShared}
          helpUrl={app.help_url}
          formatHint={app.format_hint}
          credentialEnv={app.credential_env}
          connectionEnv={app.connection_env}
          connectionUrl={app.connection_url}
          sharedLast4={app.shared_last4}
          profile={profile}
          existingProfiles={profileNames}
          onConnected={connected}
        />
      ) : null}

      {methods.includes("oauth") && method === "oauth" ? (
        <ConnectOAuthDialog slug={app.slug} name={name} open={dialog === "connect"} onOpenChange={(open) => setDialog(open ? "connect" : null)} shared={isShared} sharedLast4={app.shared_last4} profile={profile} existingProfiles={profileNames} />
      ) : null}

      {app.auth_kind !== "none" ? (
        <DisconnectDialog
          slug={app.slug}
          name={name}
          open={dialog === "disconnect"}
          onOpenChange={(open) => setDialog(open ? "disconnect" : null)}
          authKind={app.auth_kind}
          last4={profiles.find((item) => item.profile === profile)?.last4 ?? app.last4}
          sharedAvailable={app.shared_available}
          profile={profile}
          onDisconnected={disconnected}
        />
      ) : null}
    </div>
  );
}
