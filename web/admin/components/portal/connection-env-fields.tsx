"use client";

import { useId } from "react";

import { Input } from "../ui/input";
import { useT } from "../../lib/i18n";
import { portalConnect } from "../../lib/i18n/messages/portalConnect";
import { MAX_ENV_VALUE, envInputType, type ConnectionVar } from "../../lib/connection-env";

export interface ConnectionEnvFieldsProps {
  vars: readonly ConnectionVar[];
  values: Record<string, string>;
  onValueChange: (name: string, value: string) => void;
  /** Shown per variable after a save attempt. */
  errors: Record<string, string>;
  /** The connection already exists: all blank keeps its saved values. */
  isExisting: boolean;
}

/** One input per variable the server lets each connection set (e.g. PORTAINER_URL). Values never come back from the API. */
export function ConnectionEnvFields({ vars, values, onValueChange, errors, isExisting }: ConnectionEnvFieldsProps) {
  const t = useT(portalConnect);
  const id = useId();
  if (vars.length === 0) return null;
  return (
    <fieldset className="flex flex-col gap-3">
      <legend className="text-[13px] font-semibold text-fg">{t("envLegend")}</legend>
      <p className="-mt-1 text-xs leading-[1.125rem] text-fg-muted">
        {t(isExisting ? "envHintExisting" : "envHintNew")}
      </p>
      {vars.map((v) => {
        const fieldId = `${id}-${v.name}`;
        const type = envInputType(v.name);
        const error = errors[v.name];
        return (
          <div key={v.name} className="flex flex-col gap-1.5">
            <label htmlFor={fieldId} className="font-mono text-[13px] text-fg">
              {v.name}
              {v.has_default ? <span className="ml-2 font-sans text-xs text-fg-muted">{t("optional")}</span> : null}
            </label>
            <Input
              id={fieldId}
              type={type}
              autoComplete={type === "password" ? "new-password" : "off"}
              spellCheck={false}
              maxLength={MAX_ENV_VALUE}
              required={!v.has_default && !isExisting}
              value={values[v.name] ?? ""}
              placeholder={v.has_default ? t("serverDefault") : type === "url" ? "https://..." : undefined}
              aria-invalid={Boolean(error) || undefined}
              aria-describedby={error ? `${fieldId}-error` : undefined}
              onChange={(event) => onValueChange(v.name, event.target.value)}
              className="font-mono text-[13px]"
            />
            {error ? (
              <p id={`${fieldId}-error`} className="text-xs text-cut">
                {error}
              </p>
            ) : null}
          </div>
        );
      })}
    </fieldset>
  );
}
