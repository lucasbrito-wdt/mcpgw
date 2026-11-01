// Ported from design/v5/src/components/signature/Switchboard.tsx on 2026-10-31.
"use client";

import { motion } from "motion/react";
import { useCallback, useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";

import { MonoLabel } from "../ui/mono-label";
import { usePrefersReducedMotion } from "../../hooks/usePrefersReducedMotion";
import { useT } from "../../lib/i18n";
import { signature } from "../../lib/i18n/messages/signature";
import { DURATION, EASE_SWITCH } from "../../lib/motion";
import type { Agent, SwitchboardServer } from "../../lib/types";
import { cn } from "../../lib/utils";

import { rowGeometry, stackGeometry, toBox, type Box, type SwitchboardGeometry } from "./switchboard-geometry";
import { AgentPill, CoreNode, KEY_STYLE, KeyNode, ServerTile, keyStateOf, type KeyState } from "./switchboard-nodes";
import { Packet, useTraffic, type SwitchboardPacket } from "./switchboard-traffic";

export type { SwitchboardPacket } from "./switchboard-traffic";
export { PACKET_COLOR } from "./switchboard-traffic";

export interface SwitchboardProps {
  /** Headline inside the panel, left of the diagram like the art ("Servidores"). */
  title: ReactNode;
  titleAs?: "h1" | "h2";
  description?: ReactNode;
  /** Buttons under the description. */
  actions?: ReactNode;
  /** Servers in display order; the first `maxTiles` get a tile, the rest are counted in the caption. */
  servers: readonly SwitchboardServer[];
  /** Agents on the left column (the portal shows up to four). */
  agents: readonly Agent[];
  /** Calls to animate. replay loops them as a demo; live plays each new id once (admin). */
  traffic?: readonly SwitchboardPacket[];
  mode?: "replay" | "live";
  /** Tile link target. Defaults to the portal server detail. */
  hrefFor?: (slug: string) => string;
  agentsCaption?: string;
  /** Defaults to the server count, e.g. "12 servers". */
  serversCaption?: string;
  /** Tiles in the row layout (5 per row). The stacked mobile layout shows the first 10. */
  maxTiles?: number;
  className?: string;
}

const STACKED_TILES = 10;
const CAPTION_SPACE = 30; // mono caption under core and key, kept clear of the vertical spine
const INTRO_MS = 1900;
const KEY_FLASH_MS = 900;

const defaultHref = (slug: string) => `/portal/servers/${slug}/`;

function measure(root: HTMLElement): SwitchboardGeometry | null {
  const origin = root.getBoundingClientRect();
  const pick = (name: string) => root.querySelector(`[data-sb="${name}"]`);
  const boxOf = (el: Element | null): Box | null => (el ? toBox(el.getBoundingClientRect(), origin) : null);
  const core = boxOf(pick("core"));
  const key = boxOf(pick("key"));
  const agentsCol = boxOf(pick("agents"));
  const serversCol = boxOf(pick("servers"));
  const grid = boxOf(pick("grid"));
  if (!core || !key || !agentsCol || !serversCol || !grid) return null;

  const agentEls = Array.from(root.querySelectorAll<HTMLElement>('[data-sb="agent"]'));
  const vertical = core.top >= agentsCol.bottom;
  if (vertical) {
    return stackGeometry({
      width: origin.width,
      height: origin.height,
      agentIds: agentEls.map((el) => el.dataset.id ?? ""),
      agentsRow: agentsCol,
      core: { ...core, bottom: core.bottom + CAPTION_SPACE },
      key,
      keyColumn: { ...key, bottom: key.bottom + CAPTION_SPACE },
      serversRow: serversCol,
    });
  }

  const tiles = Array.from(root.querySelectorAll<HTMLElement>('[data-sb="tile"]'))
    .filter((el) => el.getClientRects().length > 0)
    .map((el) => ({ slug: el.dataset.slug ?? "", box: toBox(el.getBoundingClientRect(), origin) }));
  const gridEl = pick("grid");
  const rowGap = gridEl ? parseFloat(getComputedStyle(gridEl).rowGap) || 12 : 12;
  return rowGeometry({
    width: origin.width,
    height: origin.height,
    agents: Object.fromEntries(agentEls.map((el) => [el.dataset.id ?? "", toBox(el.getBoundingClientRect(), origin)])),
    core,
    key,
    tiles,
    grid,
    rowGap,
  });
}

const WIRE = { fill: "none", stroke: "var(--color-core)", strokeOpacity: 0.55, strokeWidth: 1.5, strokeLinecap: "round", strokeDasharray: "0.1 5" } as const;

/**
 * The portal hero and the admin live view: agents on the left, the luminous gateway, the key it
 * resolves, and the servers grid fading at the edge. Hover or focus a tile to light its route and
 * see which key a call would use; packets replay recent calls (or stream live ones in the admin).
 */
export function Switchboard({
  title,
  titleAs: Title = "h1",
  description,
  actions,
  servers,
  agents,
  traffic = [],
  mode = "replay",
  hrefFor = defaultHref,
  agentsCaption,
  serversCaption,
  maxTiles = 20,
  className,
}: SwitchboardProps) {
  const t = useT(signature);
  const reduceMotion = usePrefersReducedMotion();
  const uid = useId().replace(/:/g, "");
  const diagramRef = useRef<HTMLDivElement>(null);
  const [geometry, setGeometry] = useState<SwitchboardGeometry | null>(null);
  const [activeSlug, setActiveSlug] = useState<string | null>(null);
  const [packetKey, setPacketKey] = useState<KeyState | null>(null);
  const [introDone, setIntroDone] = useState(false);
  const flashTimer = useRef(0);

  const bySlug = useMemo(() => new Map(servers.map((s) => [s.slug, s])), [servers]);
  const tiles = servers.slice(0, maxTiles);
  const { flying, land } = useTraffic(traffic, mode, introDone && !reduceMotion);

  useEffect(() => {
    const root = diagramRef.current;
    if (!root) return;
    const update = () => setGeometry(measure(root));
    const observer = new ResizeObserver(update);
    observer.observe(root);
    document.fonts?.ready.then(update);
    const timer = window.setTimeout(() => setIntroDone(true), INTRO_MS);
    return () => {
      observer.disconnect();
      window.clearTimeout(timer);
      window.clearTimeout(flashTimer.current);
    };
  }, []);

  const onKey = useCallback(
    (packet: SwitchboardPacket) => {
      const server = bySlug.get(packet.server);
      if (!server) return;
      setPacketKey(keyStateOf(server, packet.key));
      window.clearTimeout(flashTimer.current);
      flashTimer.current = window.setTimeout(() => setPacketKey(null), KEY_FLASH_MS);
    },
    [bySlug],
  );

  const activeServer = activeSlug ? bySlug.get(activeSlug) : undefined;
  const shownKey = activeServer ? keyStateOf(activeServer) : packetKey;
  const intro = !reduceMotion && !introDone;
  const captionId = `${uid}-servers`;

  return (
    <section
      aria-labelledby={`${uid}-title`}
      className={cn(
        "@container relative isolate overflow-hidden rounded-panel border border-white/10 bg-linear-to-b from-black from-45% to-black/45 shadow-[inset_0_1px_0_rgb(255_255_255/0.06)]",
        className,
      )}
    >
      <div className="grid gap-10 p-5 @min-[40rem]:p-8 @min-[70rem]:grid-cols-[minmax(14rem,17rem)_minmax(0,1fr)] @min-[70rem]:items-center @min-[70rem]:gap-12 @min-[70rem]:p-10">
        <header className="max-w-md">
          <Title id={`${uid}-title`} className="font-display text-display text-balance text-fg">
            {title}
          </Title>
          {description ? <p className="mt-4 text-sm leading-6 text-pretty text-fg-muted">{description}</p> : null}
          {actions ? <div className="mt-6 flex flex-wrap gap-2">{actions}</div> : null}
        </header>

        <div ref={diagramRef} className="relative flex flex-col items-center gap-14 pb-2 @min-[50rem]:flex-row @min-[50rem]:justify-between @min-[50rem]:gap-8 @min-[50rem]:pb-0">
          {geometry ? (
            <svg aria-hidden className="pointer-events-none absolute inset-0 overflow-visible" width={geometry.width} height={geometry.height}>
              <defs>
                <linearGradient id={`${uid}-orbit`} gradientUnits="userSpaceOnUse" x1="0" y1={geometry.core.y + 260} x2="0" y2={geometry.core.y - 300}>
                  <stop offset="0" stopColor="var(--color-core)" stopOpacity="0" />
                  <stop offset="0.45" stopColor="var(--color-core)" stopOpacity="0.85" />
                  <stop offset="1" stopColor="var(--color-signal)" stopOpacity="0.1" />
                </linearGradient>
                {intro ? (
                  <mask id={`${uid}-draw`} maskUnits="userSpaceOnUse" x="0" y="0" width={geometry.width} height={geometry.height}>
                    {[
                      ...Object.values(geometry.agentWires).map((d) => ({ d, delay: 0 })),
                      { d: geometry.coreToKey, delay: 0.55 },
                      ...geometry.serverWires.map((d) => ({ d, delay: 0.8 })),
                    ].map(({ d, delay }, i) => (
                      <motion.path
                        key={i}
                        d={d}
                        fill="none"
                        stroke="white"
                        strokeWidth={6}
                        initial={{ pathLength: 0 }}
                        animate={{ pathLength: 1 }}
                        transition={{ duration: DURATION.draw, ease: EASE_SWITCH, delay }}
                      />
                    ))}
                  </mask>
                ) : null}
              </defs>

              <path d={geometry.orbit} fill="none" stroke={`url(#${uid}-orbit)`} strokeWidth={1.5} />

              <g mask={intro ? `url(#${uid}-draw)` : undefined}>
                {Object.entries(geometry.agentWires).map(([id, d]) => (
                  <path key={id} d={d} {...WIRE} />
                ))}
                <path d={geometry.coreToKey} {...WIRE} />
                {geometry.serverWires.map((d, i) => (
                  <path key={i} d={d} {...WIRE} />
                ))}
              </g>

              {activeServer ? <LitRoute geometry={geometry} slug={activeServer.slug} state={keyStateOf(activeServer)} still={reduceMotion} /> : null}

              {flying.map((packet) => (
                <Packet key={packet.id} packet={packet} geometry={geometry} onKey={onKey} onDone={land} />
              ))}

              <Spark orbit={geometry.orbit} still={reduceMotion} />
            </svg>
          ) : null}

          <div data-sb="agents" className="relative z-10 flex w-full flex-col items-center gap-3 @min-[50rem]:w-44 @min-[50rem]:items-stretch">
            <MonoLabel as="p" tone="quiet" id={`${uid}-agents`}>
              {agentsCaption ?? t("agentsCaption")}
            </MonoLabel>
            <ul aria-labelledby={`${uid}-agents`} className="flex flex-wrap justify-center gap-2 @min-[50rem]:flex-col @min-[50rem]:flex-nowrap">
              {agents.map((agent) => (
                <AgentPill key={agent.id} agent={agent} />
              ))}
            </ul>
          </div>

          <div aria-hidden className="contents">
            <CoreNode intro={intro} />
            <KeyNode state={shownKey} />
          </div>

          <div data-sb="servers" className="relative z-10 flex flex-col items-center gap-3 @min-[50rem]:items-start">
            <MonoLabel as="p" tone="strong" id={captionId}>
              {serversCaption ?? t.plural("serversCaption", servers.length)}
            </MonoLabel>
            <ul
              data-sb="grid"
              aria-labelledby={captionId}
              className="grid grid-cols-5 gap-2.5 mask-r-from-72% mask-b-from-62% focus-within:mask-none @min-[50rem]:gap-3"
            >
              {tiles.map((server, index) => (
                <ServerTile
                  key={server.slug}
                  server={server}
                  href={hrefFor(server.slug)}
                  active={server.slug === activeSlug}
                  wideOnly={index >= STACKED_TILES}
                  onActivate={setActiveSlug}
                />
              ))}
            </ul>
          </div>
        </div>
      </div>
    </section>
  );
}

function LitRoute({ geometry, slug, state, still }: { geometry: SwitchboardGeometry; slug: string; state: KeyState; still: boolean }) {
  const style = KEY_STYLE[state];
  const tile = geometry.tileRoutes[slug] ?? geometry.overflowRoute;
  const segment = (d: string, mode: "lit" | "broken", delay: number) =>
    mode === "lit" ? (
      <motion.path
        key={`${slug}-${delay}`}
        d={d}
        stroke={style.color}
        strokeWidth={1.75}
        initial={still ? false : { pathLength: 0 }}
        animate={{ pathLength: 1 }}
        transition={{ duration: DURATION.enter, ease: EASE_SWITCH, delay }}
      />
    ) : (
      <path key={`${slug}-${delay}`} d={d} stroke={style.color} strokeWidth={1.5} strokeDasharray="3 5" strokeOpacity={0.8} />
    );
  return (
    <g fill="none" strokeLinecap="round" className={style.drop}>
      {segment(geometry.coreToKey, style.a, 0)}
      {segment(tile, style.b, 0.12)}
    </g>
  );
}

/** The white spark riding the orbit. Parked at one point under reduced motion. */
function Spark({ orbit, still }: { orbit: string; still: boolean }) {
  return (
    <circle r={3} fill="var(--color-spark)" opacity={still ? 1 : 0} className="drop-shadow-[0_0_6px_var(--color-spark)]">
      {still ? (
        <animateMotion path={orbit} dur="1s" fill="freeze" calcMode="linear" keyPoints="0.7;0.7" keyTimes="0;1" />
      ) : (
        <>
          <animateMotion path={orbit} dur="9s" begin="0.8s" repeatCount="indefinite" calcMode="spline" keyPoints="0;1" keyTimes="0;1" keySplines="0.22 1 0.36 1" />
          <animate attributeName="opacity" values="0;1;1;0" keyTimes="0;0.06;0.85;1" dur="9s" begin="0.8s" repeatCount="indefinite" />
        </>
      )}
    </circle>
  );
}
