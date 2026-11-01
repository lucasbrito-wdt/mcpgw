// Ported from design/v5/src/components/shell/AppShell.tsx on 2026-10-31.
"use client";

import { Menu } from "lucide-react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState, type ReactNode } from "react";

import { BootSplash } from "../brand/BootSplash";
import { McpgwTile } from "../brand/McpgwMark";
import { Button } from "../ui/button";
import { Drawer } from "../ui/dialog";
import { api } from "../../lib/api";
import { useT } from "../../lib/i18n";
import { shell } from "../../lib/i18n/messages/shell";
import type { Me } from "../../lib/types";
import { cn } from "../../lib/utils";

import { LocaleSwitcher } from "./LocaleSwitcher";
import { NAV, SURFACE_HOME, SURFACE_LABEL_KEY, isActive, type Surface } from "./nav";

export interface AppShellProps {
  /** Portal (people) or admin (instance). Picks the nav; the layout picks the nebula intensity. */
  surface: Surface;
  /** Tools waiting for review; shown as the count on the "Revisão" item (admin only). */
  reviewCount?: number;
  children: ReactNode;
}

/** Surfaces whose session was already confirmed in this document: later page changes skip the splash. */
const verifiedSurfaces = new Set<Surface>();

function initialsOf(email: string): string {
  const parts = email.split("@")[0].split(/[._-]+/).filter(Boolean);
  return ((parts[0]?.[0] ?? "?") + (parts[1]?.[0] ?? "")).toUpperCase();
}

function Wordmark({ href }: { href: string }) {
  return (
    <Link href={href} className="focus-ring flex items-center gap-2 rounded-control text-[15px] font-semibold tracking-[-0.02em] text-fg">
      <McpgwTile small className="size-7" markClassName="size-4" />
      <span className="font-brand font-semibold tracking-[-0.04em]">mcpgw</span>
    </Link>
  );
}

function SurfaceSwitch({ surface }: { surface: Surface }) {
  const t = useT(shell);
  return (
    <nav aria-label={t("area")} className="grid grid-cols-2 gap-1 rounded-control border border-line p-0.5">
      {(Object.keys(SURFACE_HOME) as Surface[]).map((s) => (
        <Link
          key={s}
          href={SURFACE_HOME[s]}
          aria-current={s === surface ? "page" : undefined}
          className={cn(
            "focus-ring grid h-7 place-items-center rounded-[4px] border text-xs font-semibold transition-colors duration-150 ease-switch",
            s === surface ? "border-signal bg-signal-wash text-signal-fg" : "border-transparent text-fg-muted hover:text-fg",
          )}
        >
          {t(SURFACE_LABEL_KEY[s])}
        </Link>
      ))}
    </nav>
  );
}

interface SidebarBodyProps {
  surface: Surface;
  me: Me | null;
  reviewCount?: number;
  onNavigate?: () => void;
}

function SidebarBody({ surface, me, reviewCount, onNavigate }: SidebarBodyProps) {
  const pathname = usePathname();
  const t = useT(shell);
  const showSwitch = surface === "admin" || me?.is_admin === true;
  return (
    <div className="flex h-full flex-col gap-5 p-3">
      <div className="flex h-10 items-center px-1.5">
        <Wordmark href={SURFACE_HOME[surface]} />
      </div>
      {showSwitch ? <SurfaceSwitch surface={surface} /> : null}
      <nav aria-label={t(SURFACE_LABEL_KEY[surface])} className="flex flex-col gap-5">
        {NAV[surface].map((section) => (
          <div key={section.labelKey}>
            <p className="mb-1.5 px-2.5 text-[11px] font-semibold tracking-[0.06em] text-fg-muted uppercase">{t(section.labelKey)}</p>
            <ul className="flex flex-col gap-0.5">
              {section.items.map((item) => {
                const active = isActive(item, pathname);
                const Icon = item.icon;
                const count = item.badge === "review" ? reviewCount : undefined;
                return (
                  <li key={item.href}>
                    <Link
                      href={item.href}
                      onClick={onNavigate}
                      aria-current={active ? "page" : undefined}
                      className={cn(
                        "focus-ring flex h-8 items-center gap-2.5 rounded-control px-2.5 text-[13px] font-medium transition-colors duration-150 ease-switch",
                        active ? "bg-signal-wash text-signal-fg" : "text-fg-muted hover:bg-white/5 hover:text-fg",
                      )}
                    >
                      <Icon className="size-4 shrink-0" aria-hidden />
                      <span className="flex-1 truncate">{t(item.labelKey)}</span>
                      {count ? (
                        <span className="rounded-[4px] bg-warn-wash px-1.5 font-mono text-[11px] leading-5 text-warn" aria-label={t.plural("pending", count)}>
                          {count}
                        </span>
                      ) : null}
                    </Link>
                  </li>
                );
              })}
            </ul>
          </div>
        ))}
      </nav>

      <div className="mt-auto flex flex-col gap-1 border-t border-line pt-3">
        <div className="flex items-center gap-2.5 px-2.5 py-2">
          <span className="grid size-7 shrink-0 place-items-center rounded-full bg-guide text-[11px] font-semibold text-signal-fg" aria-hidden>
            {me ? initialsOf(me.email) : ""}
          </span>
          <span className="min-w-0 leading-tight">
            <span className="block min-h-4 truncate text-[13px] font-medium text-fg">{me?.email ?? ""}</span>
            <span className="block truncate text-xs text-fg-muted">{surface === "admin" || me?.is_admin ? t("admin") : t("member")}</span>
          </span>
        </div>
        <LocaleSwitcher className="mx-2.5 self-start" />
      </div>
    </div>
  );
}

/**
 * Airbyte Cloud shell: 200px sidebar with caps section labels, Portal | Admin switch, account at the
 * bottom. Below lg the sidebar becomes a left drawer behind a top bar. The nebula is behind it all
 * (root layout), so the sidebar is translucent ink.
 */
export function AppShell({ surface, reviewCount, children }: AppShellProps) {
  const t = useT(shell);
  const [menuOpen, setMenuOpen] = useState(false);
  const [me, setMe] = useState<Me | null>(null);
  const [sessionChecked, setSessionChecked] = useState(() => verifiedSurfaces.has(surface));

  useEffect(() => {
    let cancelled = false;
    // A 401 redirects to the login page inside api(); any other failure leaves the account block empty.
    api<Me>(surface, "/me")
      .then((result) => {
        if (!cancelled) setMe(result);
      })
      .catch(() => {})
      .finally(() => {
        verifiedSurfaces.add(surface);
        if (!cancelled) setSessionChecked(true);
      });
    return () => {
      cancelled = true;
    };
  }, [surface]);

  if (!sessionChecked) return <BootSplash />;

  return (
    <div className="min-h-dvh">
      <a
        href="#main"
        className="focus-ring sr-only z-50 rounded-control bg-panel px-3 py-2 text-sm text-fg focus:not-sr-only focus:fixed focus:top-3 focus:left-3"
      >
        {t("skipToContent")}
      </a>

      <aside className="fixed inset-y-0 left-0 z-30 hidden w-50 border-r border-line bg-ink/80 backdrop-blur-md lg:block">
        <SidebarBody surface={surface} me={me} reviewCount={reviewCount} />
      </aside>

      <header className="sticky top-0 z-30 flex h-14 items-center justify-between border-b border-line bg-ink/80 px-4 backdrop-blur-md lg:hidden">
        <Wordmark href={SURFACE_HOME[surface]} />
        <Button variant="ghost" size="icon" aria-label={t("openNavigation")} aria-expanded={menuOpen} onClick={() => setMenuOpen(true)}>
          <Menu aria-hidden />
        </Button>
      </header>
      <Drawer open={menuOpen} onOpenChange={setMenuOpen} title={t("navigation")} side="left" bareHeader className="w-60 gap-0 p-0">
        <SidebarBody surface={surface} me={me} reviewCount={reviewCount} onNavigate={() => setMenuOpen(false)} />
      </Drawer>

      <main id="main" tabIndex={-1} className="outline-none lg:pl-50">
        <div className="mx-auto w-full max-w-[90rem] px-4 py-5 sm:px-6 sm:py-6 lg:px-8 lg:py-8">{children}</div>
      </main>
    </div>
  );
}
