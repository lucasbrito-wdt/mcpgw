// Ported from design/v5/src/components/signature/route-hero.tsx on 2026-10-31.
"use client";

import { KeyRound, LockOpen, Unplug, Users, type LucideIcon } from "lucide-react";
import { AnimatePresence, LayoutGroup, useReducedMotion } from "motion/react";
import { useId, type ReactNode } from "react";

import { McpgwTile } from "../brand/McpgwMark";
import { ServiceLogo } from "../brand/ServiceLogo";
import { MotionSpan } from "../ui/motion-span";
import { KEY_LABEL, PROBLEM_LABEL } from "../../lib/format";
import { useT } from "../../lib/i18n";
import { signature } from "../../lib/i18n/messages/signature";
import { DURATION, EASE_SWITCH } from "../../lib/motion";
import type { ConnectionProblem, KeyKind, SwitchboardServer } from "../../lib/types";
import { cn } from "../../lib/utils";

export interface RouteHeroProps {
  server: Pick<SwitchboardServer, "slug" | "name"> & { host: string };
  /** Key the viewer's calls use today. */
  current: KeyKind;
  /** Put the personal key in the socket: the "Conectar a sua" action is hovered or focused. */
  preview?: boolean;
  /** Account behind the shared key ("bot@wdt"). Present means a shared key exists. */
  sharedAccount?: string;
  /** Account behind the viewer's own key, once connected. */
  personalAccount?: string;
  /** The viewer's key is broken; the route shows where. */
  problem?: ConnectionProblem;
  /** Who calls: the signed-in user's e-mail. */
  caller: string;
  className?: string;
}

type Socket = "personal" | "shared" | "open" | "none";

/** Colors are static classes: no style attribute may reach the exported HTML (CSP, plan gap G26). */
const KEY_LOOK: Record<Socket, { icon: LucideIcon; border: string; disc: string }> = {
  personal: { icon: KeyRound, border: "border-core", disc: "bg-core text-white shadow-[0_0_24px_-4px_var(--color-core)]" },
  shared: { icon: Users, border: "border-signal-fg", disc: "bg-panel text-signal-fg ring-2 ring-signal-fg" },
  open: { icon: LockOpen, border: "border-ok", disc: "bg-panel text-ok ring-1 ring-ok/50" },
  none: { icon: Unplug, border: "border-fg-subtle", disc: "border border-dashed border-fg-subtle text-fg-muted" },
};

const PROBLEM_LOOK: Record<ConnectionProblem, { border: string; ring: string; text: string }> = {
  revoked: { border: "border-cut", ring: "shadow-[0_0_0_2px_var(--color-cut)]", text: "text-cut" },
  expired: { border: "border-warn", ring: "shadow-[0_0_0_2px_var(--color-warn)]", text: "text-warn" },
  scopes: { border: "border-warn", ring: "shadow-[0_0_0_2px_var(--color-warn)]", text: "text-warn" },
};

function Node({ slot, title, sub }: { slot: ReactNode; title: ReactNode; sub: ReactNode }) {
  return (
    <li className="flex min-w-0 items-center gap-4 @min-[36rem]:flex-col @min-[36rem]:gap-3 @min-[36rem]:text-center">
      <div className="relative grid size-12 shrink-0 place-items-center">{slot}</div>
      <div className="min-w-0">
        <div className="text-[15px] leading-5 font-semibold text-fg">{title}</div>
        <div className="mt-0.5 truncate text-[13px] leading-5 text-fg-muted">{sub}</div>
      </div>
    </li>
  );
}

function Span({ border, broken = false }: { border: string; broken?: boolean }) {
  return (
    <li
      aria-hidden
      className={cn(
        "my-1 ml-[23px] h-7 w-0 border-l-2 transition-[border-color] duration-300 ease-switch",
        "@min-[36rem]:mx-3 @min-[36rem]:mt-[23px] @min-[36rem]:mb-0 @min-[36rem]:h-0 @min-[36rem]:w-auto @min-[36rem]:border-t-2 @min-[36rem]:border-l-0",
        broken ? "border-dashed" : "border-solid",
        border,
      )}
    />
  );
}

/**
 * Server detail header: you, the gateway, the key it resolves, the server. Hovering "Conectar a sua"
 * (preview) slides your key into the socket and pushes the shared one aside, the exact change
 * connecting makes. Stands up vertically in narrow containers.
 */
export function RouteHero({ server, current, preview = false, sharedAccount, personalAccount, problem, caller, className }: RouteHeroProps & { size?: "hero" }) {
  const t = useT(signature);
  const reduceMotion = useReducedMotion();
  const uid = useId();
  const socket: Socket = preview && current !== "open" ? "personal" : current;
  const reserve = socket === "personal" && sharedAccount ? "shared" : null;
  const broken = socket === "personal" && !preview ? problem : undefined;
  const keyBorder = broken ? PROBLEM_LOOK[broken].border : KEY_LOOK[socket].border;
  const swap = { duration: reduceMotion ? 0 : DURATION.enter, ease: EASE_SWITCH };
  const enter = reduceMotion ? { opacity: 0 } : { opacity: 0, y: -18, scale: 0.6 };

  const keyTitle = broken ? PROBLEM_LABEL[broken] : socket === "personal" && preview && current !== "personal" ? t("yourKey") : KEY_LABEL[socket];
  const keySub =
    socket === "personal"
      ? (personalAccount ?? t("personalFallback"))
      : socket === "shared"
        ? (sharedAccount ?? t("sharedFallback"))
        : socket === "open"
          ? t("openSub")
          : t("noneSub");

  const chip = (kind: Socket, small: boolean) => {
    const look = KEY_LOOK[kind];
    const Icon = look.icon;
    return (
      <MotionSpan
        key={kind}
        layoutId={`${uid}-${kind}`}
        initial={enter}
        animate={{ opacity: small ? 0.55 : 1, y: 0, scale: 1 }}
        exit={enter}
        transition={swap}
        className={cn(
          "absolute grid place-items-center rounded-full",
          small ? "-top-3 -right-4 size-6 opacity-55" : "inset-0",
          look.disc,
          broken && !small && PROBLEM_LOOK[broken].ring,
        )}
      >
        <Icon className={small ? "size-3" : "size-5"} strokeWidth={1.8} aria-hidden />
      </MotionSpan>
    );
  };

  return (
    <div className={cn("@container", className)}>
      <ol
        aria-label={t("routeLabel")}
        className="flex flex-col @min-[36rem]:grid @min-[36rem]:grid-cols-[auto_minmax(1.5rem,1fr)_auto_minmax(1.5rem,1fr)_auto_minmax(1.5rem,1fr)_auto] @min-[36rem]:items-start"
      >
        <Node
          slot={
            <span className="grid size-12 place-items-center rounded-full bg-white/6 ring-1 ring-line">
              <span className="size-2.5 rounded-full bg-fg" />
            </span>
          }
          title={t("you")}
          sub={caller}
        />
        <Span border="border-fg-muted" />
        <Node
          slot={
            <McpgwTile className="size-12" markClassName="size-6" />
          }
          title="mcpgw"
          sub={t("picksKey")}
        />
        <Span border={keyBorder} broken={socket === "none" || broken === "expired"} />
        <LayoutGroup id={uid}>
          <Node
            slot={
              <AnimatePresence initial={false}>
                {chip(socket, false)}
                {reserve ? chip(reserve, true) : null}
              </AnimatePresence>
            }
            title={
              <span className="grid">
                <AnimatePresence initial={false}>
                  <MotionSpan key={keyTitle} className={cn("col-start-1 row-start-1", broken && PROBLEM_LOOK[broken].text)} initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }} transition={swap}>
                    {keyTitle}
                  </MotionSpan>
                </AnimatePresence>
              </span>
            }
            sub={
              <>
                {keySub}
                {sharedAccount ? (
                  <span className={cn("block text-xs text-fg-muted", !reserve && "invisible")}>{t("sharedReserve")}</span>
                ) : null}
              </>
            }
          />
        </LayoutGroup>
        <Span border={keyBorder} broken={socket === "none" || Boolean(broken)} />
        <Node slot={<ServiceLogo slug={server.slug} name={server.name} size="lg" />} title={server.name} sub={server.host} />
      </ol>
    </div>
  );
}
