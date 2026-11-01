// Ported from design/v5/src/components/portal/disconnect-dialog.tsx on 2026-10-31.
"use client";

import { useState } from "react";

import { Button } from "../ui/button";
import { Dialog } from "../ui/dialog";
import { api } from "../../lib/api";
import { useT } from "../../lib/i18n";
import { portalCommon } from "../../lib/i18n/messages/portalCommon";
import { portalDetail } from "../../lib/i18n/messages/portalDetail";
import { DEFAULT_PROFILE, profileQuery, type AuthKind } from "../../lib/portal-types";

import { Note } from "./connect-common";
import { describeConnectError } from "./connect-request";

export interface DisconnectDialogProps {
  slug: string;
  name: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  authKind: AuthKind;
  last4?: string | null;
  /** A shared connection exists: the next call falls back to it. */
  sharedAvailable: boolean;
  /** Which of the viewer's connections goes away. */
  profile?: string;
  onDisconnected: () => void;
}

/** Confirms removing the viewer's own key and says exactly which key the next call falls back to. */
export function DisconnectDialog({ slug, name, open, onOpenChange, authKind, last4, sharedAvailable, profile = DEFAULT_PROFILE, onDisconnected }: DisconnectDialogProps) {
  const t = useT(portalDetail);
  const tCommon = useT(portalCommon);
  const [isBusy, setIsBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function confirm() {
    setIsBusy(true);
    setError(null);
    try {
      await api<void>("portal", `/connections/${encodeURIComponent(slug)}${profileQuery(profile)}`, { method: "DELETE" });
      onDisconnected();
    } catch (caught) {
      setError(describeConnectError(caught).message);
    } finally {
      setIsBusy(false);
    }
  }

  const what = authKind === "header" && last4 ? t("disconnectWhatToken", { last4 }) : t("disconnectWhatConnection", { name });
  const after =
    profile !== DEFAULT_PROFILE
      ? t("disconnectAfterProfile", { profile })
      : sharedAvailable
        ? t("disconnectAfterShared")
        : t("disconnectAfterNone", { name });

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      size="sm"
      title={profile === DEFAULT_PROFILE ? t("disconnectTitle", { name }) : t("disconnectTitleProfile", { profile, name })}
      description={after}
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {tCommon("cancel")}
          </Button>
          <Button variant="danger" loading={isBusy} onClick={() => void confirm()}>
            {tCommon("disconnect")}
          </Button>
        </>
      }
    >
      <p className="text-[13px] leading-5 text-fg-muted">{what} {t("disconnectTail")}</p>
      {error ? (
        <Note tone="cut" title={t("disconnectFailed")}>
          {error}
        </Note>
      ) : null}
    </Dialog>
  );
}
