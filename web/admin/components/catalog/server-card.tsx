// Ported from design/v5/src/components/catalog/server-card.tsx on 2026-10-31.
// Data comes from the portal API (PortalApp); host, last call and "down" states are not in the contract.
"use client";

import { ChevronRight } from "lucide-react";
import Link from "next/link";

import { ServiceLogo } from "../brand/ServiceLogo";
import { RouteLine } from "../signature/RouteLine";
import { Pill } from "../ui/badge";
import { formatDateTime, KEY_LABEL } from "../../lib/format";
import { getLocale, translate, useT } from "../../lib/i18n";
import { catalog } from "../../lib/i18n/messages/catalog";
import { acceptsBothMethods, appName, credentialWordsOf, keyOf, type AuthKind, type PortalApp } from "../../lib/portal-types";
import { cn } from "../../lib/utils";

export type ServerCardDensity = "detailed" | "standard" | "compact";

export interface ServerCardProps {
  app: PortalApp;
  /** detailed: servers you use (route + last use). standard: ready to use. compact: one row, needs a key. */
  density: ServerCardDensity;
  className?: string;
}

/** Read at access time so the label follows the active locale (the module loads before it is known). */
export const AUTH_LABEL: Readonly<Record<AuthKind, string>> = {
  get none() {
    return translate(getLocale(), catalog, "auth_none");
  },
  get oauth() {
    return translate(getLocale(), catalog, "auth_oauth");
  },
  get header() {
    return translate(getLocale(), catalog, "auth_header");
  },
};

/** Auth label of one app: both methods read "OAuth ou token". */
export function authLabelOf(app: Pick<PortalApp, "auth_kind" | "auth_methods">): string {
  return acceptsBothMethods(app) ? translate(getLocale(), catalog, "auth_both") : AUTH_LABEL[app.auth_kind];
}

export function connectHref(slug: string): string {
  return `/portal/connect/?slug=${encodeURIComponent(slug)}`;
}

const CARD =
  "focus-ring group relative flex rounded-card border border-line bg-panel/90 transition-[background-color,border-color] duration-150 ease-switch hover:border-line-strong hover:bg-[color-mix(in_oklab,var(--color-panel)_92%,white)]";

/** Airbyte catalog card for one server: light logo square, name, pills. Three densities, one per portal group. */
export function ServerCard({ app, density, className }: ServerCardProps) {
  const t = useT(catalog);
  const name = appName(app);
  const target = connectHref(app.slug);
  const key = keyOf(app);

  if (density === "compact") {
    return (
      <Link href={target} className={cn(CARD, "items-center gap-3 px-3 py-2.5", className)}>
        <ServiceLogo slug={app.slug} name={name} variant="catalog" size="sm" />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-[13px] font-medium text-fg">{name}</span>
          <span className="block truncate text-xs text-signal-fg">{acceptsBothMethods(app) ? t("auth_both") : app.auth_kind === "oauth" ? t("signInOAuth") : t("paste", { what: credentialWordsOf(app.credential_env).label.toLowerCase() })}</span>
        </span>
        <ChevronRight className="size-4 shrink-0 text-fg-muted transition-transform duration-150 ease-switch motion-safe:group-hover:translate-x-0.5" aria-hidden />
      </Link>
    );
  }

  if (density === "standard") {
    return (
      <Link href={target} className={cn(CARD, "items-center gap-3 p-3.5", className)}>
        <ServiceLogo slug={app.slug} name={name} variant="catalog" size="md" />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-semibold text-fg">{name}</span>
          <span className="mt-1.5 flex flex-wrap items-center gap-x-2 gap-y-1">
            <Pill kind={key} />
            <span className="text-xs text-fg-muted">{t(app.tool_count === 1 ? "tools_one" : "tools_other", { n: app.tool_count })}</span>
          </span>
        </span>
      </Link>
    );
  }

  return (
    <Link href={target} className={cn(CARD, "flex-col gap-4 p-4", className)}>
      <span className="flex items-center gap-3">
        <ServiceLogo slug={app.slug} name={name} variant="catalog" size="md" />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-semibold text-fg">{name}</span>
          <span className="block truncate text-xs text-fg-muted">{app.account_label ?? authLabelOf(app)}</span>
        </span>
        <ChevronRight className="size-4 shrink-0 text-fg-muted transition-transform duration-150 ease-switch motion-safe:group-hover:translate-x-0.5" aria-hidden />
      </span>
      <span className="flex items-center gap-2.5">
        <RouteLine size="mini" keyKind={key} />
        <span className={cn("text-xs font-medium", key === "personal" ? "text-core" : "text-fg-muted")}>{KEY_LABEL[key]}</span>
      </span>
      <span className="flex min-w-0 flex-col gap-0.5 border-t border-line pt-3 text-xs">
        <span className="text-fg-muted">{t("lastUsed")}</span>
        <span className="truncate text-fg">{app.last_used_at ? formatDateTime(app.last_used_at) : t("neverUsed")}</span>
      </span>
    </Link>
  );
}
