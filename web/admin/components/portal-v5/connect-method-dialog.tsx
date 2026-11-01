"use client";

import { KeyRound, ShieldCheck } from "lucide-react";
import { useState } from "react";

import { OptionCard } from "../admin-servers/option-card";
import { Button } from "../ui/button";
import { Dialog } from "../ui/dialog";
import { useT } from "../../lib/i18n";
import { portalCommon } from "../../lib/i18n/messages/portalCommon";
import { portalDetail } from "../../lib/i18n/messages/portalDetail";
import type { AuthMethod } from "../../lib/portal-types";

const OPTIONS = [
  { value: "oauth", title: "methodOauthTitle", description: "methodOauthDescription", icon: ShieldCheck },
  { value: "token", title: "methodTokenTitle", description: "methodTokenDescription", icon: KeyRound },
] as const;

export interface ConnectMethodDialogProps {
  name: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Option selected when the dialog opens. */
  initial?: AuthMethod;
  onChoose: (method: AuthMethod) => void;
}

/** First step of connecting to a server that accepts OAuth and token: the viewer picks one. */
export function ConnectMethodDialog({ name, open, onOpenChange, initial = "oauth", onChoose }: ConnectMethodDialogProps) {
  const t = useT(portalDetail);
  return (
    <Dialog open={open} onOpenChange={onOpenChange} size="md" title={t("methodTitle", { name })} description={t("methodDescription", { name })}>
      <MethodChoice initial={initial} onCancel={() => onOpenChange(false)} onChoose={onChoose} />
    </Dialog>
  );
}

function MethodChoice({ initial, onCancel, onChoose }: { initial: AuthMethod; onCancel: () => void; onChoose: (method: AuthMethod) => void }) {
  const t = useT(portalDetail);
  const tCommon = useT(portalCommon);
  const [method, setMethod] = useState<AuthMethod>(initial);
  return (
    <form
      className="flex flex-col gap-5"
      onSubmit={(event) => {
        event.preventDefault();
        onChoose(method);
      }}
    >
      <fieldset className="min-w-0">
        <legend className="sr-only">{t("methodLegend")}</legend>
        <div className="grid gap-2 sm:grid-cols-2">
          {OPTIONS.map((option) => (
            <OptionCard
              key={option.value}
              name="connect-method"
              value={option.value}
              checked={method === option.value}
              onSelect={(next) => setMethod(next as AuthMethod)}
              title={t(option.title)}
              description={t(option.description)}
              icon={option.icon}
            />
          ))}
        </div>
      </fieldset>
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" onClick={onCancel}>
          {tCommon("cancel")}
        </Button>
        <Button type="submit" variant="primary">
          {t("continue")}
        </Button>
      </div>
    </form>
  );
}
