// Ported from design/v5/src/components/portal/{tools-panel,tool-row}.tsx on 2026-10-31.
// Tool names and descriptions come from the upstream: they are rendered as text nodes only.
"use client";

import { ChevronDown, KeyRound, SearchX } from "lucide-react";
import { AnimatePresence, motion } from "motion/react";
import { useState } from "react";

import { Button } from "../ui/button";
import { Pill, type PillKind } from "../ui/badge";
import { EmptyState } from "../ui/empty-state";
import { SearchField } from "../ui/input";
import { SegmentedTabs } from "../ui/segmented-tabs";
import { countLabel } from "../../hooks/countLabel";
import { usePrefersReducedMotion } from "../../hooks/usePrefersReducedMotion";
import { levelLabel } from "../../lib/format";
import { useT } from "../../lib/i18n";
import { portalDetail } from "../../lib/i18n/messages/portalDetail";
import { DURATION, EASE_SWITCH } from "../../lib/motion";
import type { PortalTool } from "../../lib/portal-types";
import type { AccessLevel } from "../../lib/types";
import { cn } from "../../lib/utils";

type LevelFilter = "all" | "read" | "write" | "destructive";

const LEVEL_PILL: Partial<Record<AccessLevel, PillKind>> = { read: "read", write: "write", destructive: "destroy" };
const FILTERS: readonly LevelFilter[] = ["all", "read", "write", "destructive"];

export interface ToolsAccordionProps {
  serverName: string;
  tools: readonly PortalTool[];
  /** The viewer has no key yet, so an empty list means "connect first" rather than "nothing here". */
  needsConnection: boolean;
  /** Opens the connect flow in the empty "connect first" state. */
  onConnect?: () => void;
}

function matches(tool: PortalTool, query: string): boolean {
  const q = query.trim().toLowerCase();
  return !q || `${tool.name} ${tool.description ?? ""}`.toLowerCase().includes(q);
}

function ToolRow({ tool, isOpen, onToggle }: { tool: PortalTool; isOpen: boolean; onToggle: () => void }) {
  const reduceMotion = usePrefersReducedMotion();
  const panelId = `tool-${tool.name.replace(/\W/g, "-")}`;
  const pill = LEVEL_PILL[tool.level];
  const summary = (
    <>
      <code className="truncate font-mono text-[13px] text-fg">{tool.name}</code>
      {pill ? <Pill kind={pill} /> : <span className="text-xs text-fg-muted">{levelLabel(tool.level)}</span>}
    </>
  );

  if (!tool.description) {
    return <li className="flex items-center justify-between gap-3 px-3 py-2.5 sm:px-4">{summary}</li>;
  }

  return (
    <li className={cn("transition-colors duration-150 ease-switch", isOpen && "bg-white/3")}>
      <button
        type="button"
        aria-expanded={isOpen}
        aria-controls={panelId}
        onClick={onToggle}
        className="group grid w-full cursor-pointer grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-x-3 px-3 py-2.5 text-left outline-none transition-colors duration-150 ease-switch hover:bg-white/4 focus-visible:bg-white/4 focus-visible:ring-2 focus-visible:ring-signal-fg focus-visible:ring-inset active:bg-white/6 sm:px-4"
      >
        {summary}
        <ChevronDown className={cn("size-4 text-fg-muted transition-transform duration-200 ease-switch group-hover:text-fg", isOpen && "rotate-180")} aria-hidden />
      </button>
      <AnimatePresence initial={false}>
        {isOpen ? (
          <motion.div
            id={panelId}
            key="panel"
            initial={reduceMotion ? { opacity: 0 } : { height: 0, opacity: 0 }}
            animate={reduceMotion ? { opacity: 1 } : { height: "auto", opacity: 1 }}
            exit={reduceMotion ? { opacity: 0 } : { height: 0, opacity: 0 }}
            transition={{ duration: DURATION.enter, ease: EASE_SWITCH }}
            className="overflow-hidden"
          >
            <p className="max-w-prose px-3 pb-4 text-[13px] leading-5 text-pretty text-fg-muted sm:px-4">{tool.description}</p>
          </motion.div>
        ) : null}
      </AnimatePresence>
    </li>
  );
}

/** Dense, searchable tool list of one server, with a level filter and drawn empty states. */
export function ToolsAccordion({ serverName, tools, needsConnection, onConnect }: ToolsAccordionProps) {
  const t = useT(portalDetail);
  const [query, setQuery] = useState("");
  const [level, setLevel] = useState<LevelFilter>("all");
  const [openName, setOpenName] = useState<string | null>(null);

  const heading = (
    <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
      <h2 id="tools-title" className="text-base font-semibold tracking-[-0.01em] text-fg">
        {t("toolsHeading")}
      </h2>
      <span className="font-mono text-xs text-fg-muted">{tools.length}</span>
    </div>
  );

  if (tools.length === 0) {
    return (
      <section aria-labelledby="tools-title" className="flex flex-col gap-4">
        {heading}
        {needsConnection ? (
          <EmptyState
            icon={KeyRound}
            title={t("connectToSeeTitle")}
            action={
              onConnect ? (
                <Button variant="secondary" onClick={onConnect}>
                  {t("connectYours")}
                </Button>
              ) : null
            }
          >
            {t("connectToSeeBody", { name: serverName })}
          </EmptyState>
        ) : (
          <EmptyState icon={SearchX} title={t("noVisibleTitle")}>
            {t("noVisibleBody", { name: serverName })}
          </EmptyState>
        )}
      </section>
    );
  }

  const searched = tools.filter((tool) => matches(tool, query));
  const visible = level === "all" ? searched : searched.filter((tool) => tool.level === level);
  const countOf = (l: LevelFilter) => (l === "all" ? searched.length : searched.filter((tool) => tool.level === l).length);

  return (
    <section aria-labelledby="tools-title" className="flex flex-col gap-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        {heading}
        <SearchField value={query} onValueChange={setQuery} label={t("searchTools", { name: serverName })} placeholder={t("searchToolsPlaceholder")} className="sm:w-80" />
      </div>
      <SegmentedTabs
        label={t("toolLevel")}
        controls="tool-list"
        size="sm"
        value={level}
        onValueChange={setLevel}
        items={FILTERS.map((l) => ({ value: l, label: l === "all" ? t("levelAll") : levelLabel(l), count: countOf(l) }))}
      />

      <p aria-live="polite" className="sr-only">
        {countLabel(t, "toolCount", visible.length)}
      </p>

      {visible.length > 0 ? (
        <ul id="tool-list" className="divide-y divide-line overflow-hidden rounded-card border border-line bg-panel/90">
          {visible.map((tool) => (
            <ToolRow key={tool.name} tool={tool} isOpen={openName === tool.name} onToggle={() => setOpenName(openName === tool.name ? null : tool.name)} />
          ))}
        </ul>
      ) : (
        <EmptyState
          icon={SearchX}
          title={query.trim() ? t("noToolsQuery", { query: query.trim() }) : t("noToolsLevel", { level: levelLabel(level === "all" ? "none" : level).toLowerCase() })}
          action={
            <Button
              variant="secondary"
              onClick={() => {
                setQuery("");
                setLevel("all");
              }}
            >
              {t("clearSearchLevel")}
            </Button>
          }
        >
          {t("searchScope", { n: tools.length, name: serverName })}
        </EmptyState>
      )}
    </section>
  );
}
