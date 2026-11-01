// Replaces design/v5/src/components/admin-servers/catalog-step.tsx. The v5 step is a catalog grid plus "Testar URL";
// there is no catalog in the contract, so step 1 is the custom-server form with "Testar URL" (POST /servers/test; never blocks Continuar).
"use client";

import { Container, Globe } from "lucide-react";
import { useState } from "react";

import { api, ApiError } from "../../lib/api";
import { useT } from "../../lib/i18n";
import { serversWizard } from "../../lib/i18n/messages/serversWizard";
import type { ProbeResult } from "../../lib/stats-types";
import { Button } from "../ui/button";

import { Input } from "../ui/input";

import { Field, describedBy } from "./field";
import { OptionCard } from "./option-card";
import { formatProbe, type ProbeMessage } from "./probe-format";
import { ProbeLine } from "./probe-line";
import { StdioFields } from "./stdio-fields";
import { slugHint, type ServerDraft, type ServerDraftErrors, type WizardTransport } from "./wizard-state";

export interface ServerStepProps {
  draft: ServerDraft;
  onChange: (draft: ServerDraft) => void;
  errors: ServerDraftErrors;
  disabled?: boolean;
}

const MONO = "font-mono text-[13px]";

/** Step 1: slug, where the server runs (HTTP or Docker stdio, never the host) and the optional category. */
export function ServerStep({ draft, onChange, errors, disabled }: ServerStepProps) {
  const t = useT(serversWizard);
  const set = (patch: Partial<ServerDraft>) => onChange({ ...draft, ...patch });
  const [testing, setTesting] = useState(false);
  // Tied to the URL it was run against, so editing the URL hides a stale result.
  const [probe, setProbe] = useState<{ url: string; message: ProbeMessage } | null>(null);
  const url = draft.url.trim();

  async function testUrl() {
    setTesting(true);
    setProbe(null);
    let message: ProbeMessage;
    try {
      message = formatProbe(await api<ProbeResult>("admin", "/servers/test", { method: "POST", body: { url, transport: draft.transport } }), "wizard");
    } catch (e) {
      message = { tone: "cut", text: e instanceof ApiError ? (e.fields?.url ?? e.message) : t("testUrlFailed") };
    }
    setProbe({ url, message });
    setTesting(false);
  }
  return (
    <div className="flex flex-col gap-5">
      <Field id="server-slug" label={t("slugLabel")} error={errors.slug} hint={t("slugHintFull", { hint: slugHint() })}>
        <Input id="server-slug" value={draft.slug} onChange={(e) => set({ slug: e.target.value.toLowerCase() })} disabled={disabled} autoComplete="off" spellCheck={false} maxLength={63} placeholder="github" className={MONO} aria-invalid={Boolean(errors.slug) || undefined} aria-describedby={describedBy("server-slug", { hint: true, error: errors.slug })} />
      </Field>

      <fieldset className="min-w-0" disabled={disabled}>
        <legend className="mb-2 text-[13px] font-semibold text-fg">{t("transportLegend")}</legend>
        <div className="grid gap-2 sm:grid-cols-2">
          <OptionCard name="transport" value="auto" checked={draft.transport === "auto"} onSelect={(v) => set({ transport: v as WizardTransport })} title={t("transportHttpTitle")} description={t("transportHttpDesc")} icon={Globe} />
          <OptionCard name="transport" value="stdio" checked={draft.transport === "stdio"} onSelect={(v) => set({ transport: v as WizardTransport })} title={t("transportDockerTitle")} description={t("transportDockerDesc")} icon={Container} />
        </div>
      </fieldset>

      {draft.transport === "auto" ? (
        <Field id="server-url" label={t("urlLabel")} error={errors.url} hint={t("urlHint")}>
          <div className="flex gap-2">
            <Input id="server-url" type="url" value={draft.url} onChange={(e) => set({ url: e.target.value })} disabled={disabled} autoComplete="off" spellCheck={false} placeholder="https://api.githubcopilot.com/mcp/" className={MONO} aria-invalid={Boolean(errors.url) || undefined} aria-describedby={describedBy("server-url", { hint: true, error: errors.url })} />
            <Button variant="secondary" size="lg" className="h-9 text-xs" disabled={disabled || !url} loading={testing} onClick={() => void testUrl()}>
              {t("testUrl")}
            </Button>
          </div>
          {probe && probe.url === url ? <ProbeLine message={probe.message} className="mt-2" /> : null}
        </Field>
      ) : (
        <>
          <Field id="server-image" label={t("imageLabel")} error={errors.dockerImage} hint={t("imageHint")}>
            <Input id="server-image" value={draft.dockerImage} onChange={(e) => set({ dockerImage: e.target.value })} disabled={disabled} autoComplete="off" spellCheck={false} placeholder="mcp/filesystem:1" className={MONO} aria-invalid={Boolean(errors.dockerImage) || undefined} aria-describedby={describedBy("server-image", { hint: true, error: errors.dockerImage })} />
          </Field>
          <StdioFields draft={draft} onChange={set} errors={errors} disabled={disabled} />
        </>
      )}

      <Field id="server-category" label={t("categoryLabel")} optional error={errors.category} hint={t("categoryHint")}>
        <Input id="server-category" value={draft.category} onChange={(e) => set({ category: e.target.value.toLowerCase() })} disabled={disabled} autoComplete="off" spellCheck={false} maxLength={32} className={MONO} aria-invalid={Boolean(errors.category) || undefined} aria-describedby={describedBy("server-category", { hint: true, error: errors.category })} />
      </Field>
    </div>
  );
}
