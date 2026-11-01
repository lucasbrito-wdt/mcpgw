"use client";

import { ServerCard, type ServerCardDensity } from "../catalog/server-card";
import { useT } from "../../lib/i18n";
import { portalServers } from "../../lib/i18n/messages/portalServers";
import { appName, type PortalApp } from "../../lib/portal-types";
import { cn } from "../../lib/utils";

export interface AppCardProps {
  app: PortalApp;
  density: ServerCardDensity;
  /** Present only on the grid: ticks the card for the bulk "Disconnect selected". */
  selected?: boolean;
  onSelectedChange?: (selected: boolean) => void;
  className?: string;
}

/** Catalog card plus, for the viewer's own connections, a checkbox that joins the bulk selection. */
export function AppCard({ app, density, selected = false, onSelectedChange, className }: AppCardProps) {
  const t = useT(portalServers);
  const canSelect = onSelectedChange !== undefined && app.connection === "personal" && density !== "compact";
  return (
    <div className={cn("relative", className)}>
      <ServerCard app={app} density={density} className="h-full" />
      {canSelect ? (
        <label
          className={cn(
            "absolute right-2.5 grid size-8 cursor-pointer place-items-center rounded-control hover:bg-white/8",
            density === "detailed" ? "bottom-2" : "top-1/2 -translate-y-1/2",
          )}
        >
          <input
            type="checkbox"
            checked={selected}
            onChange={(event) => onSelectedChange(event.target.checked)}
            aria-label={t("selectApp", { name: appName(app) })}
            className="size-4 accent-signal focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-signal-fg"
          />
        </label>
      ) : null}
    </div>
  );
}
