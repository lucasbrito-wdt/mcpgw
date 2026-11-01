// Ported from design/v5/src/components/portal/server-hero.tsx on 2026-10-31.
// "Disabled" and "Down" states are not in the portal contract (disabled servers are 404).
"use client";

import { Bot, ChevronRight, Unplug, X } from "lucide-react";
import Link from "next/link";
import { useState, type ReactNode } from "react";

import { authLabelOf } from "../catalog/server-card";
import { ConnectSplitButton } from "../portal/ConnectSplitButton";
import { ServiceLogo } from "../brand/ServiceLogo";
import { RouteLine } from "../signature/RouteLine";
import { Badge, Pill, type PillKind } from "../ui/badge";
import { Button } from "../ui/button";
import { buttonVariants } from "../ui/button-variants";
import { countLabel } from "../../hooks/countLabel";
import { formatDateTime } from "../../lib/format";
import { useT } from "../../lib/i18n";
import { portalCommon } from "../../lib/i18n/messages/portalCommon";
import { portalDetail } from "../../lib/i18n/messages/portalDetail";
import {
  acceptsBothMethods,
  connectionStatusLabel,
  keyOf,
  problemOf,
  problemReason,
  type ConnectAudience,
  type PortalAppDetail,
} from "../../lib/portal-types";
import type { ConnectionProblem, KeyKind } from "../../lib/types";
import { cn } from "../../lib/utils";

/** What the viewer's calls to this server do right now. */
export type HeroState = { kind: "open" } | { kind: "shared" } | { kind: "none" } | { kind: "personal" } | { kind: "problem"; problem: ConnectionProblem };

export function heroStateOf(app: PortalAppDetail): HeroState {
  if (app.auth_kind === "none") return { kind: "open" };
  if (app.connection === "personal") {
    const problem = problemOf(app.status);
    return problem ? { kind: "problem", problem } : { kind: "personal" };
  }
  return { kind: keyOf(app) === "shared" ? "shared" : "none" };
}

export interface ServerHeroProps {
  app: PortalAppDetail;
  name: string;
  /** E-mail of the signed-in user: who calls, at the left end of the route. */
  caller: string;
  isAdmin: boolean;
  /** Confirmation after connecting or disconnecting, read out by screen readers. */
  notice?: string | null;
  onDismissNotice: () => void;
  onConnect: (audience: ConnectAudience) => void;
  onDisconnect: () => void;
}

const ROUTE_KEY: Record<HeroState["kind"], KeyKind> = { open: "open", shared: "shared", none: "none", personal: "personal", problem: "personal" };

function statusPill(state: HeroState): PillKind {
  return state.kind === "problem" ? state.problem : state.kind;
}

function Verdict({ title, tone = "fg", children }: { title: string; tone?: "fg" | "warn" | "cut"; children: ReactNode }) {
  return (
    <div className="min-w-0 max-w-2xl">
      <p className={cn("text-[15px] font-semibold", tone === "warn" ? "text-warn" : tone === "cut" ? "text-cut" : "text-fg")}>{title}</p>
      <p className="mt-1 text-sm leading-6 text-pretty text-fg-muted">{children}</p>
    </div>
  );
}

/** Facts about the viewer's own key. Token keys show only the last 4; OAuth shows the account. */
function KeyFacts({ app }: { app: PortalAppDetail }) {
  const t = useT(portalDetail);
  const connectedAt = app.timeline.find((entry) => entry.event === "connected")?.ts;
  const facts: [string, ReactNode][] = [];
  if (app.account_label) facts.push([t("factAccount"), app.account_label]);
  facts.push([t("factKey"), app.last4 ? <span key="key" className="font-mono tracking-wide">••••{app.last4}</span> : "OAuth 2.0"]);
  facts.push([t("factExpires"), app.expires_at ? formatDateTime(app.expires_at) : t("noExpiry")]);
  if (connectedAt) facts.push([t("factConnectedAt"), formatDateTime(connectedAt)]);
  if (app.scope_granted) facts.push([t("factScope"), <span key="scope" className="font-mono text-xs break-all">{app.scope_granted}</span>]);
  return (
    <dl className="grid grid-cols-2 gap-x-6 gap-y-3 sm:grid-cols-4">
      {facts.map(([term, value]) => (
        <div key={term} className="min-w-0">
          <dt className="text-xs text-fg-muted">{term}</dt>
          <dd className="mt-0.5 truncate text-[13px] text-fg">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

/**
 * Server detail header. The route is the hero: you, mcpgw, the key, the server. Hovering or focusing the
 * connect action previews the swap (your key in the socket, the shared one aside). One primary action per state.
 */
export function ServerHero({ app, name, caller, isAdmin, notice, onDismissNotice, onConnect, onDisconnect }: ServerHeroProps) {
  const t = useT(portalDetail);
  const tCommon = useT(portalCommon);
  const [isPreviewing, setIsPreviewing] = useState(false);
  const state = heroStateOf(app);
  const isPersonal = state.kind === "personal";
  const problem = state.kind === "problem" ? state.problem : undefined;
  const sharedToken = app.shared_last4 ? t("sharedToken", { last4: app.shared_last4 }) : null;
  const sharedAccount = app.shared_available ? (sharedToken ?? t("sharedGeneric")) : undefined;
  const sharedName = app.shared_available ? (sharedToken ?? t("sharedAdminAccount")) : "";

  const connect = (label?: string) => (
    <ConnectSplitButton authKind={app.auth_kind} isAdmin={isAdmin} label={label} onConnect={onConnect} onPreviewChange={setIsPreviewing} />
  );

  let verdict: ReactNode;
  let actions: ReactNode;
  switch (state.kind) {
    case "open":
      verdict = (
        <Verdict title={t("verdictOpenTitle")}>{t("verdictOpenBody", { name })}</Verdict>
      );
      actions = (
        <Link href="/portal/agents/" className={buttonVariants({ variant: "secondary", size: "lg" })}>
          <Bot aria-hidden />
          {tCommon("connectAgent")}
        </Link>
      );
      break;
    case "shared":
      verdict = (
        <Verdict title={t("verdictSharedTitle")}>{t("verdictSharedBody", { account: sharedName })}</Verdict>
      );
      actions = connect();
      break;
    case "none":
      verdict = (
        <Verdict title={t("verdictNoneTitle")} tone="warn">
          {t("verdictNoneBody")}
        </Verdict>
      );
      actions = connect();
      break;
    case "personal":
      verdict = (
        <Verdict title={t("verdictPersonalTitle")}>
          {app.shared_available ? t("verdictPersonalShared", { account: sharedName }) : t("verdictPersonalNoShared", { name })}
        </Verdict>
      );
      actions = (
        <>
          <Button variant="ghost" size="lg" onClick={onDisconnect} className="hover:text-cut">
            <Unplug aria-hidden />
            {tCommon("disconnect")}
          </Button>
          {connect(app.auth_kind === "header" && !acceptsBothMethods(app) ? t("replaceToken") : t("reconnect"))}
        </>
      );
      break;
    case "problem":
      verdict = (
        <Verdict title={connectionStatusLabel(app.status)} tone={state.problem === "revoked" ? "cut" : "warn"}>
          {[problemReason(app.status, name), app.revoked_reason ? t("problemMotive", { reason: app.revoked_reason }) : null, t(app.shared_available ? "problemTailShared" : "problemTailNoShared")]
            .filter(Boolean)
            .join(" ")}
        </Verdict>
      );
      actions = (
        <>
          <Button variant="ghost" size="lg" onClick={onDisconnect} className="hover:text-cut">
            <Unplug aria-hidden />
            {tCommon("disconnect")}
          </Button>
          {connect(t("reconnect"))}
        </>
      );
      break;
  }

  return (
    <section
      aria-labelledby="server-title"
      className="@container relative isolate overflow-hidden rounded-panel border border-white/10 bg-linear-to-b from-black from-45% to-black/45 shadow-[inset_0_1px_0_rgb(255_255_255/0.06)]"
    >
      <header className="flex flex-col gap-5 p-5 @min-[40rem]:p-8 @min-[40rem]:pb-6">
        <nav aria-label={t("breadcrumb")} className="flex items-center gap-1.5 text-[13px] text-fg-muted">
          <Link href="/portal/" className="focus-ring rounded-[4px] hover:text-fg">
            {t("crumbServers")}
          </Link>
          <ChevronRight className="size-3.5" aria-hidden />
          <span aria-current="page" className="text-fg">
            {name}
          </span>
        </nav>
        <div className="flex flex-wrap items-center gap-x-5 gap-y-4">
          <ServiceLogo slug={app.slug} name={name} variant="catalog" size="xl" />
          <div className="min-w-0 flex-1">
            <h1 id="server-title" className="font-display text-display text-balance text-fg">
              {name}
            </h1>
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <Pill kind={statusPill(state)} />
              {isPersonal && app.status === "expiring" ? <Badge tone="warn">{connectionStatusLabel("expiring")}</Badge> : null}
              <span className="text-xs text-fg-muted">
                {countLabel(t, "toolCount", app.tools.length)}
              </span>
            </div>
          </div>
        </div>
      </header>

      <div className="border-y border-line bg-white/[0.015] px-5 py-8 @min-[40rem]:px-8 @min-[40rem]:py-10">
        <RouteLine
          size="hero"
          server={{ slug: app.slug, name, host: app.credential_env ? t("routeHostEnv", { env: app.credential_env }) : authLabelOf(app) }}
          current={ROUTE_KEY[state.kind]}
          preview={isPreviewing}
          sharedAccount={sharedAccount}
          personalAccount={isPersonal || problem ? (app.account_label ?? (app.last4 ? `••••${app.last4}` : "OAuth 2.0")) : undefined}
          problem={problem}
          caller={caller}
          className="mx-auto max-w-4xl"
        />
      </div>

      <div className="flex flex-col gap-5 p-5 @min-[40rem]:px-8 @min-[40rem]:py-6">
        <div aria-live="polite" className="empty:hidden">
          {notice ? (
            <div className="flex items-start gap-3 rounded-card border border-ok/30 bg-ok-wash px-3 py-2.5 text-[13px] text-fg">
              <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-ok" aria-hidden />
              <span className="min-w-0 flex-1">{notice}</span>
              <Button variant="ghost" size="icon-sm" aria-label={t("closeNotice")} onClick={onDismissNotice} className="-my-1">
                <X aria-hidden />
              </Button>
            </div>
          ) : null}
        </div>
        <div className="flex flex-col gap-5 @min-[48rem]:flex-row @min-[48rem]:items-center @min-[48rem]:justify-between">
          {verdict}
          <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>
        </div>
        {isPersonal || problem ? (
          <div className="border-t border-line pt-5">
            <KeyFacts app={app} />
          </div>
        ) : null}
      </div>
    </section>
  );
}
