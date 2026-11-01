"use client";

import { ChevronDown, KeyRound, Plug, User, Users } from "lucide-react";
import { useState } from "react";

import { Button } from "../ui/button";
import { Popover } from "../ui/popover";
import { useT } from "../../lib/i18n";
import { portalDetail } from "../../lib/i18n/messages/portalDetail";
import { connectMenu, type AuthKind, type ConnectAudience } from "../../lib/portal-types";

export interface ConnectSplitButtonProps {
  authKind: AuthKind;
  /** From `GET /me`. "Everyone" is rendered only when this is true; the server enforces it too. */
  isAdmin: boolean;
  /** Main label: "Connect account" by default, "Reconnect" or "Replace token" when a key exists. */
  label?: string;
  onConnect: (audience: ConnectAudience) => void;
  /** Hover or focus on the main action: the hero previews the key swap. */
  onPreviewChange?: (previewing: boolean) => void;
}

/**
 * "Connect account": the main action connects for the viewer alone; admins get a menu with "Everyone".
 * Servers that need no key (auth_kind none) render nothing.
 */
export function ConnectSplitButton({ authKind, isAdmin, label, onConnect, onPreviewChange }: ConnectSplitButtonProps) {
  const t = useT(portalDetail);
  const [open, setOpen] = useState(false);
  const menu = connectMenu({ auth_kind: authKind, is_admin: isAdmin });
  if (menu.length === 0) return null;

  const preview = onPreviewChange && {
    onPointerEnter: () => onPreviewChange(true),
    onPointerLeave: () => onPreviewChange(false),
    onFocus: () => onPreviewChange(true),
    onBlur: () => onPreviewChange(false),
  };

  const main = (
    <Button variant="primary" size="lg" onClick={() => onConnect("personal")} className={menu.includes("shared") ? "rounded-r-none" : undefined} {...preview}>
      <Plug aria-hidden />
      {label ?? t("connectAccount")}
    </Button>
  );
  if (!menu.includes("shared")) return main;

  const pick = (audience: ConnectAudience) => {
    setOpen(false);
    onConnect(audience);
  };

  return (
    <div className="inline-flex">
      {main}
      <Popover
        open={open}
        onOpenChange={setOpen}
        title={t("connectFor")}
        className="w-72"
        trigger={
          <Button variant="primary" size="lg" aria-label={t("chooseAudience")} className="rounded-l-none border-l-white/25 px-2">
            <ChevronDown aria-hidden />
          </Button>
        }
      >
        <div className="flex flex-col">
          <button
            type="button"
            onClick={() => pick("personal")}
            className="focus-ring flex cursor-pointer items-start gap-2.5 rounded-control px-2 py-2 text-left hover:bg-white/5"
          >
            <User className="mt-0.5 size-4 shrink-0 text-fg-muted" aria-hidden />
            <span>
              <span className="block text-[13px] font-semibold text-fg">{t("justMe")}</span>
              <span className="block text-xs text-fg-muted">{t("justMeHint")}</span>
            </span>
          </button>
          <button
            type="button"
            onClick={() => pick("shared")}
            className="focus-ring flex cursor-pointer items-start gap-2.5 rounded-control px-2 py-2 text-left hover:bg-white/5"
          >
            <Users className="mt-0.5 size-4 shrink-0 text-fg-muted" aria-hidden />
            <span>
              <span className="flex items-center gap-1.5 text-[13px] font-semibold text-fg">
                {t("everyone")}
                <KeyRound className="size-3 text-warn" aria-hidden />
              </span>
              <span className="block text-xs text-fg-muted">{t("everyoneHint")}</span>
            </span>
          </button>
        </div>
      </Popover>
    </div>
  );
}
