"use client";

import { Plus, X } from "lucide-react";

import type { StdioEnvRow } from "../../lib/admin-types";
import { useT } from "../../lib/i18n";
import { serversWizard } from "../../lib/i18n/messages/serversWizard";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Toggle } from "../ui/toggle";

import { Field, describedBy } from "./field";
import type { ServerDraft, ServerDraftErrors } from "./wizard-state";

const MONO = "font-mono text-[13px]";

export interface StdioFieldsProps {
  draft: ServerDraft;
  onChange: (patch: Partial<ServerDraft>) => void;
  errors: ServerDraftErrors;
  disabled?: boolean;
}

function EnvRows({ rows, onChange, disabled }: { rows: StdioEnvRow[]; onChange: (rows: StdioEnvRow[]) => void; disabled?: boolean }) {
  const t = useT(serversWizard);
  const update = (index: number, patch: Partial<StdioEnvRow>) => onChange(rows.map((row, i) => (i === index ? { ...row, ...patch } : row)));
  return (
    <div className="flex flex-col gap-2">
      {rows.map((row, index) => (
        <div key={index} className="flex gap-2">
          <Input
            aria-label={t("envNameAria", { n: index + 1 })}
            name={`stdio-env-key-${index}`}
            value={row.name}
            onChange={(e) => update(index, { name: e.target.value })}
            disabled={disabled}
            autoComplete="off"
            autoCapitalize="characters"
            spellCheck={false}
            placeholder="PORTAINER_URL"
            className={MONO}
          />
          {/* Secret: "new-password" is what stops Chrome from offering the gateway login here; "off" alone is ignored on password fields. */}
          <Input
            type="password"
            aria-label={t("envValueAria", { n: index + 1 })}
            name={`stdio-env-secret-${index}`}
            value={row.value}
            onChange={(e) => update(index, { value: e.target.value })}
            disabled={disabled}
            autoComplete="new-password"
            data-1p-ignore
            data-lpignore="true"
            spellCheck={false}
            placeholder={t("valuePlaceholder")}
            className={MONO}
          />
          <Button variant="ghost" size="sm" disabled={disabled} aria-label={t("envRemoveAria", { n: index + 1 })} onClick={() => onChange(rows.filter((_, i) => i !== index))} className="h-9 hover:text-cut">
            <X aria-hidden />
          </Button>
        </div>
      ))}
      <div>
        <Button variant="secondary" size="sm" disabled={disabled} onClick={() => onChange([...rows, { name: "", value: "" }])}>
          <Plus aria-hidden />
          {t("addVariable")}
        </Button>
      </div>
    </div>
  );
}

/** Docker-only options of step 1: command, network and the fixed environment (values are write-only secrets). */
export function StdioFields({ draft, onChange, errors, disabled }: StdioFieldsProps) {
  const t = useT(serversWizard);
  return (
    <>
      <Field id="server-command" label={t("commandLabel")} optional hint={t("commandHint")}>
        <Input id="server-command" value={draft.command} onChange={(e) => onChange({ command: e.target.value })} disabled={disabled} autoComplete="off" spellCheck={false} className={MONO} aria-describedby={describedBy("server-command", { hint: true })} />
      </Field>

      <div className="flex items-start justify-between gap-4 rounded-card border border-line bg-panel/70 p-3">
        <div className="min-w-0">
          <p className="text-[13px] font-semibold text-fg">
            {t("networkTitle")}
          </p>
          <p className="mt-1 text-xs leading-5 text-fg-muted">{t("networkDesc")}</p>
        </div>
        <Toggle checked={draft.network} onCheckedChange={(network) => onChange({ network })} label={t("networkToggle")} disabled={disabled} />
      </div>

      <fieldset className="flex min-w-0 flex-col gap-2" disabled={disabled}>
        <legend className="mb-1 text-[13px] font-semibold text-fg">
          {t("envLegend")}<span className="ml-1.5 font-normal text-fg-muted">{t("optional")}</span>
        </legend>
        <EnvRows rows={draft.env} onChange={(env) => onChange({ env })} disabled={disabled} />
        {errors.env ? (
          <p role="alert" className="text-xs leading-5 text-cut">
            {errors.env}
          </p>
        ) : (
          <p className="text-xs leading-5 text-fg-muted">{t("envHint")}</p>
        )}
      </fieldset>
    </>
  );
}
