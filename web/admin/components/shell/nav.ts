// Ported from design/v5/src/components/shell/nav.ts on 2026-10-31.
import {
  Activity,
  Blocks,
  Bot,
  KeyRound,
  LayoutDashboard,
  MonitorSmartphone,
  ScrollText,
  ServerCog,
  Settings,
  ShieldCheck,
  Table2,
  Users,
  type LucideIcon,
} from "lucide-react";

import type { shell } from "../../lib/i18n/messages/shell";

/** Key in the `shell` catalog; AppShell translates it. */
export type ShellKey = keyof typeof shell.en;

export type Surface = "portal" | "admin";

export interface NavItem {
  href: string;
  labelKey: ShellKey;
  icon: LucideIcon;
  /** Pending work count shown on the right; AppShell fills it from its `reviewCount` prop. */
  badge?: "review";
  /** Extra path prefixes that keep the item active (detail pages). */
  also?: string[];
}

export interface NavSection {
  labelKey: ShellKey;
  items: NavItem[];
}

export const SURFACE_HOME: Record<Surface, string> = { portal: "/portal/", admin: "/admin/" };
export const SURFACE_LABEL_KEY: Record<Surface, ShellKey> = { portal: "surfacePortal", admin: "surfaceAdmin" };

export const NAV: Record<Surface, NavSection[]> = {
  portal: [
    {
      labelKey: "navForYou",
      items: [
        { href: "/portal/", labelKey: "navServers", icon: Blocks, also: ["/portal/connect/"] },
        { href: "/portal/agents/", labelKey: "navAgents", icon: Bot },
      ],
    },
  ],
  admin: [
    {
      labelKey: "navInstance",
      items: [
        { href: "/admin/", labelKey: "navOverview", icon: LayoutDashboard },
        { href: "/admin/servers/", labelKey: "navServers", icon: ServerCog, also: ["/admin/servers/view/"] },
        { href: "/admin/review/", labelKey: "navReview", icon: ShieldCheck, badge: "review" },
        { href: "/admin/access/", labelKey: "navAccess", icon: Table2 },
        { href: "/admin/connections/", labelKey: "navConnections", icon: KeyRound },
        { href: "/admin/clients/", labelKey: "navClients", icon: MonitorSmartphone },
        { href: "/admin/activity/", labelKey: "navLogs", icon: Activity },
      ],
    },
    {
      labelKey: "navAdministration",
      items: [
        { href: "/admin/users/", labelKey: "navUsers", icon: Users },
        { href: "/admin/log/", labelKey: "navAdminLog", icon: ScrollText },
        { href: "/admin/settings/", labelKey: "navSettings", icon: Settings },
      ],
    },
  ],
};

export function isActive(item: NavItem, pathname: string): boolean {
  const path = pathname.endsWith("/") ? pathname : `${pathname}/`;
  return path === item.href || (item.also ?? []).some((prefix) => path.startsWith(prefix));
}
