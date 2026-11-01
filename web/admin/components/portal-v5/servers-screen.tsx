// Ported from design/v5/src/app/portal/_components/servers-screen.tsx and app/portal/page.tsx on 2026-10-31.
// Data from GET /portal/api/connections (search, category, auth and sort run on the server; tabs group the loaded items).
"use client";

import { Bot, SearchX, TriangleAlert } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { useEffect, useRef, useState } from "react";

import { ServerCard, type ServerCardDensity } from "../catalog/server-card";
import { AppCard } from "../portal/AppCard";
import { ALL_CATEGORIES, CategoryRail } from "../portal/CategoryRail";
import { BulkBar } from "../portal/BulkBar";
import { Switchboard } from "../signature/Switchboard";
import { Button } from "../ui/button";
import { buttonVariants } from "../ui/button-variants";
import { Dialog } from "../ui/dialog";
import { EmptyState } from "../ui/empty-state";
import { SearchField } from "../ui/input";
import { SegmentedTabs } from "../ui/segmented-tabs";
import { Skeleton } from "../ui/skeleton";
import { useToast } from "../ui/toast";
import { countLabel } from "../../hooks/countLabel";
import { usePrefersReducedMotion } from "../../hooks/usePrefersReducedMotion";
import { api } from "../../lib/api";
import { useT } from "../../lib/i18n";
import { portalCommon } from "../../lib/i18n/messages/portalCommon";
import { portalServers } from "../../lib/i18n/messages/portalServers";
import { DURATION, EASE_SWITCH } from "../../lib/motion";
import { appName, chunkSlugs, keyOf, problemOf, relationOf, type PortalApp, type Relation } from "../../lib/portal-types";
import type { SwitchboardServer } from "../../lib/types";

import { AttentionStrip } from "./attention-strip";
import { AuthFilter, type AuthFilterValue } from "./auth-filter";
import { SWITCHBOARD_AGENTS } from "./clients";
import { useDebouncedValue, usePortalApps, type AppSort } from "./use-portal-data";

type Tab = "all" | "mine" | "available" | "unconnected";
type Group = Exclude<Relation, "failing">;
type GroupKey = "groupOwnTitle" | "groupOwnText" | "groupAvailableTitle" | "groupAvailableText" | "groupUnconnectedTitle" | "groupUnconnectedText";

/** Which relations each tab shows. "In use by you" includes the failing ones (shown in the strip). */
const TAB_RELATIONS: Record<Tab, readonly Relation[]> = {
  all: ["own", "failing", "available", "unconnected"],
  mine: ["own", "failing"],
  available: ["available"],
  unconnected: ["unconnected"],
};

const TAB_LABEL = { all: "tabAll", mine: "tabMine", available: "tabAvailable", unconnected: "tabUnconnected" } as const;

const SORT_ITEMS = [
  { value: "name", label: "sortName" },
  { value: "last_used", label: "sortLastUsed" },
] as const;

const GROUPS: { relation: Group; title: GroupKey; text: GroupKey; density: ServerCardDensity; grid: string }[] = [
  {
    relation: "own",
    title: "groupOwnTitle",
    text: "groupOwnText",
    density: "detailed",
    grid: "grid gap-3 sm:grid-cols-2 xl:grid-cols-3",
  },
  {
    relation: "available",
    title: "groupAvailableTitle",
    text: "groupAvailableText",
    density: "standard",
    grid: "grid gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4",
  },
  {
    relation: "unconnected",
    title: "groupUnconnectedTitle",
    text: "groupUnconnectedText",
    density: "compact",
    grid: "grid gap-2 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4",
  },
];

const SKELETONS = ["a", "b", "c", "d", "e", "f"];

interface Board {
  source: PortalApp[];
  servers: SwitchboardServer[];
  total: number;
}

function toBoardServer(app: PortalApp): SwitchboardServer {
  return { slug: app.slug, name: appName(app), key: keyOf(app), problem: app.connection === "personal" ? problemOf(app.status) : undefined };
}

const sumOf = (summary: Record<string, number | undefined>) => Object.values(summary).reduce<number>((total, n) => total + (n ?? 0), 0);

/** Portal home: the switchboard, then attention strip, relation tabs, filters and the grouped catalog. */
export function ServersScreen() {
  const t = useT(portalServers);
  const tCommon = useT(portalCommon);
  const toast = useToast();
  const reduceMotion = usePrefersReducedMotion();
  const searchParams = useSearchParams();
  const announced = useRef(false);

  const [tab, setTab] = useState<Tab>("all");
  const [queryText, setQueryText] = useState("");
  const [category, setCategory] = useState(ALL_CATEGORIES);
  const [auth, setAuth] = useState<AuthFilterValue>("");
  const [sort, setSort] = useState<AppSort>("name");
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [isBusy, setIsBusy] = useState(false);
  const [knownCategories, setKnownCategories] = useState<string[]>([]);
  const [board, setBoard] = useState<Board | null>(null);

  const q = useDebouncedValue(queryText, 300);
  const apps = usePortalApps({ q, category, auth, sort });
  const { items, loading, failed, nextCursor, summary } = apps;

  const isFiltered = q.trim() !== "" || category !== ALL_CATEGORIES || auth !== "";

  // Static export: the page title is the default locale; this one follows the active locale.
  useEffect(() => {
    document.title = `${t("pageTitle")} | mcpgw`;
  }, [t]);

  // Callback redirect (plan gap G16): /portal/?connected=<slug>.
  useEffect(() => {
    const slug = searchParams.get("connected");
    if (!slug || announced.current) return;
    announced.current = true;
    toast(t("connectedToast", { slug }), { tone: "ok" });
    window.history.replaceState(null, "", window.location.pathname);
  }, [searchParams, toast, t]);

  // The category ruler only grows, so picking a category does not hide the others.
  const seen = items.flatMap((app) => (app.category ? [app.category] : []));
  const unseen = seen.filter((c, i) => !knownCategories.includes(c) && seen.indexOf(c) === i);
  if (unseen.length > 0) setKnownCategories([...knownCategories, ...unseen].sort());

  // The switchboard shows the whole catalog: it follows the unfiltered list only.
  if (!loading && !failed && !isFiltered && board?.source !== items) {
    setBoard({ source: items, servers: items.map(toBoardServer), total: sumOf(summary) });
  }

  const inTab = (app: PortalApp) => TAB_RELATIONS[tab].includes(relationOf(app));
  const visible = items.filter(inTab);
  const failing = visible.filter((app) => relationOf(app) === "failing");
  // Counts are exact only when every page is loaded.
  const tabCount = (tabId: Tab) => (nextCursor ? undefined : items.filter((app) => TAB_RELATIONS[tabId].includes(relationOf(app))).length);

  const sortItems = SORT_ITEMS.map((item) => ({ value: item.value, label: t(item.label) }));

  const selectedSlugs = items.filter((app) => selected.has(app.slug) && app.connection === "personal").map((app) => app.slug);

  const clearAll = () => {
    setQueryText("");
    setCategory(ALL_CATEGORIES);
    setAuth("");
    setTab("all");
  };

  const toggleSelected = (slug: string, on: boolean) => {
    const next = new Set(selected);
    if (on) next.add(slug);
    else next.delete(slug);
    setSelected(next);
  };

  async function disconnectSelected() {
    setIsBusy(true);
    let disconnected = 0;
    let skipped = 0;
    try {
      for (const slugs of chunkSlugs(selectedSlugs)) {
        const result = await api<{ disconnected: string[]; skipped: string[] }>("portal", "/connections/disconnect", { method: "POST", body: { slugs } });
        disconnected += result.disconnected.length;
        skipped += result.skipped.length;
      }
      toast([countLabel(t, "disconnected", disconnected), skipped > 0 ? t("skipped", { n: skipped }) : null].filter(Boolean).join(" "), { tone: "ok" });
    } catch {
      toast(t("disconnectFailed"), { tone: "cut" });
    } finally {
      setIsBusy(false);
      setConfirmOpen(false);
      setSelected(new Set());
      apps.reload();
    }
  }

  return (
    <div className="flex flex-col gap-8">
      {board ? (
        <Switchboard
          title={t("pageTitle")}
          description={countLabel(t, "boardDescription", board.total)}
          actions={
            <Link href="/portal/agents/" className={buttonVariants({ variant: "secondary" })}>
              <Bot aria-hidden />
              {tCommon("connectAgent")}
            </Link>
          }
          servers={board.servers}
          agents={SWITCHBOARD_AGENTS}
          hrefFor={(slug) => `/portal/connect/?slug=${encodeURIComponent(slug)}`}
        />
      ) : (
        <Skeleton className="h-80 rounded-panel" aria-hidden />
      )}

      <div className="flex flex-col gap-6">
        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
            <SegmentedTabs
              label={t("showServers")}
              controls="server-groups"
              value={tab}
              onValueChange={setTab}
              items={(Object.keys(TAB_LABEL) as Tab[]).map((tabId) => ({ value: tabId, label: t(TAB_LABEL[tabId]), count: tabCount(tabId) }))}
            />
            <div className="flex items-center gap-2">
              <SearchField value={queryText} onValueChange={setQueryText} label={t("searchLabel")} placeholder={t("searchPlaceholder")} className="min-w-0 flex-1 lg:w-72 lg:flex-none" />
              <AuthFilter value={auth} onValueChange={setAuth} />
            </div>
          </div>
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <CategoryRail categories={knownCategories} value={category} onValueChange={setCategory} />
            <SegmentedTabs label={t("sortLabel")} size="sm" items={sortItems} value={sort} onValueChange={setSort} className="shrink-0" />
          </div>
        </div>

        <p aria-live="polite" className="sr-only">
          {loading ? t("loadingServers") : countLabel(t, "serverCount", visible.length)}
        </p>

        {loading ? (
          <ul className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3" aria-hidden>
            {SKELETONS.map((id) => (
              <li key={id}>
                <Skeleton className="h-28 rounded-card" />
              </li>
            ))}
          </ul>
        ) : failed ? (
          <EmptyState
            icon={TriangleAlert}
            title={t("loadFailedTitle")}
            action={
              <Button variant="secondary" onClick={apps.reload}>
                {tCommon("retry")}
              </Button>
            }
          >
            {tCommon("loadFailedBody")}
          </EmptyState>
        ) : (
          <AnimatePresence initial={false} mode="wait">
            <motion.div
              key={tab}
              id="server-groups"
              initial={{ opacity: 0, y: reduceMotion ? 0 : 6 }}
              animate={{ opacity: 1, y: 0 }}
              transition={{ duration: DURATION.enter, ease: EASE_SWITCH }}
              className="flex flex-col gap-8"
            >
              <AttentionStrip apps={failing} />

              {GROUPS.filter((group) => TAB_RELATIONS[tab].includes(group.relation)).map((group) => {
                const list = visible.filter((app) => relationOf(app) === group.relation);
                if (list.length === 0) return null;
                const titleId = `group-${group.relation}`;
                return (
                  <section key={group.relation} aria-labelledby={titleId} className="flex flex-col gap-3">
                    <header className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                      <h2 id={titleId} className="text-base font-semibold tracking-[-0.01em] text-fg">
                        {t(group.title)}
                      </h2>
                      <span className="font-mono text-xs text-fg-muted">{list.length}</span>
                      <p className="w-full text-[13px] text-fg-muted sm:w-auto sm:flex-1">{t(group.text)}</p>
                    </header>
                    <ul className={group.grid}>
                      {list.map((app) => (
                        <li key={app.slug} className="min-w-0">
                          {group.density === "compact" ? (
                            <ServerCard app={app} density="compact" />
                          ) : (
                            <AppCard
                              app={app}
                              density={group.density}
                              selected={selected.has(app.slug)}
                              onSelectedChange={(on) => toggleSelected(app.slug, on)}
                              className="h-full"
                            />
                          )}
                        </li>
                      ))}
                    </ul>
                  </section>
                );
              })}

              {visible.length === 0 ? (
                <EmptyState
                  icon={SearchX}
                  title={q.trim() ? t("emptyQuery", { q: q.trim() }) : isFiltered || tab !== "all" ? t("emptyFiltered") : t("emptyNone")}
                  action={
                    isFiltered || tab !== "all" ? (
                      <Button variant="secondary" onClick={clearAll}>
                        {t("clearFilters")}
                      </Button>
                    ) : null
                  }
                >
                  {isFiltered || tab !== "all"
                    ? tab !== "all"
                      ? t("emptySearchTabBody", { tab: t(TAB_LABEL[tab]) })
                      : t("emptySearchBody")
                    : t("emptyNoneBody")}
                  {nextCursor ? ` ${t("moreToLoad")}` : ""}
                </EmptyState>
              ) : null}

              {nextCursor ? (
                <div className="flex flex-wrap items-center justify-between gap-3 text-[13px] text-fg-muted">
                  <span>
                    {t("showing", { shown: items.length, total: sumOf(summary) })}
                  </span>
                  <Button size="sm" variant="secondary" loading={apps.loadingMore} onClick={apps.loadMore}>
                    {t("loadMore")}
                  </Button>
                </div>
              ) : isFiltered ? null : (
                <p className="text-[13px] text-fg-muted">{t("missingServer")}</p>
              )}
            </motion.div>
          </AnimatePresence>
        )}
      </div>

      <BulkBar count={selectedSlugs.length} onClear={() => setSelected(new Set())} onDisconnect={() => setConfirmOpen(true)} />

      <Dialog
        open={confirmOpen}
        onOpenChange={(open) => (isBusy ? undefined : setConfirmOpen(open))}
        size="sm"
        title={countLabel(t, "confirmTitle", selectedSlugs.length)}
        description={t("confirmBody")}
        footer={
          <>
            <Button variant="ghost" onClick={() => setConfirmOpen(false)} disabled={isBusy}>
              {tCommon("cancel")}
            </Button>
            <Button variant="danger" loading={isBusy} onClick={() => void disconnectSelected()}>
              {tCommon("disconnect")}
            </Button>
          </>
        }
      />
    </div>
  );
}
