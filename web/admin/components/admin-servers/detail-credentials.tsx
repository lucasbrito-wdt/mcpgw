"use client";

import { useState, type FormEvent } from "react";

import { useLoad, type ServerConnections, type ServerDetail } from "../../lib/admin-types";
import { api, ApiError } from "../../lib/api";
import { useT } from "../../lib/i18n";
import { errors } from "../../lib/i18n/messages/errors";
import { servers } from "../../lib/i18n/messages/servers";
import { serversForms } from "../../lib/i18n/messages/serversForms";
import { ReauthCancelled, useReauth } from "../admin/ReauthDialog";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { useToast } from "../ui/toast";

import { ConnectionEnvForm } from "./connection-env-form";
import { Field, describedBy } from "./field";
import { HeaderConfigForm } from "./header-config-form";
import { SharedConnection } from "./shared-connection";

interface ConnectionsResponse {
  servers: ServerConnections[];
}

function OAuthClientForm({ server, onSaved }: { server: ServerDetail; onSaved: () => void }) {
  const t = useT(serversForms);
  const tError = useT(errors);
  const toast = useToast();
  const reauth = useReauth();
  const [clientId, setClientId] = useState(server.oauth_client_id ?? "");
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!clientId.trim()) return setError(t("ocClientIdRequired"));
    setBusy(true);
    setError(undefined);
    const body = secret.trim() ? { client_id: clientId.trim(), client_secret: secret.trim() } : { client_id: clientId.trim() };
    try {
      await reauth.run(() => api<void>("admin", `/servers/${encodeURIComponent(server.slug)}/oauth-client`, { method: "PUT", body }));
      toast(t("ocSaved"), { tone: "ok" });
      setSecret("");
      onSaved();
    } catch (e) {
      if (!(e instanceof ReauthCancelled)) setError(e instanceof ApiError ? (e.fields?.client_id ?? e.message) : tError("generic"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form noValidate onSubmit={submit} aria-label={t("ocTitle")} className="flex flex-col gap-4 rounded-card border border-line bg-panel/70 p-4">
      <div>
        <h3 className="text-[13px] font-semibold text-fg">{t("ocTitle")}</h3>
        <p className="mt-1 text-xs text-fg-muted">
          {server.has_oauth_client ? t("ocHasClient") : t("ocNoClient")}
        </p>
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="oauth-client-id" label={t("clientId")} error={error}>
          <Input id="oauth-client-id" value={clientId} onChange={(e) => setClientId(e.target.value)} disabled={busy} autoComplete="off" spellCheck={false} className="font-mono text-[13px]" aria-invalid={Boolean(error) || undefined} aria-describedby={describedBy("oauth-client-id", { error })} />
        </Field>
        <Field id="oauth-client-secret" label={t("clientSecret")} optional hint={t("ocSecretHint")}>
          <Input id="oauth-client-secret" type="password" value={secret} onChange={(e) => setSecret(e.target.value)} disabled={busy} autoComplete="new-password" spellCheck={false} className="font-mono text-[13px]" aria-describedby={describedBy("oauth-client-secret", { hint: true })} />
        </Field>
      </div>
      <div>
        <Button variant="primary" type="submit" loading={busy}>
          {t("ocSave")}
        </Button>
      </div>
      {reauth.dialog}
    </form>
  );
}

export interface DetailCredentialsProps {
  server: ServerDetail;
  onChanged: () => void;
}

/** Credentials of one server: header config, OAuth client and the shared connection. Values are never shown. */
export function DetailCredentials({ server, onChanged }: DetailCredentialsProps) {
  const t = useT(servers);
  const connections = useLoad<ConnectionsResponse>(server.auth_kind === "none" ? null : `/connections?server=${encodeURIComponent(server.slug)}`);
  const shared = connections.data?.servers.find((s) => s.slug === server.slug)?.shared ?? null;
  const changed = () => {
    connections.reload();
    onChanged();
  };

  if (server.auth_kind === "none") {
    return <p className="rounded-card border border-line bg-panel/70 p-4 text-[13px] text-fg-muted">{t("credNone")}</p>;
  }
  return (
    <div className="flex flex-col gap-4">
      {server.credential_env ? (
        <>
          <p className="rounded-card border border-line bg-panel/70 p-4 text-[13px] leading-6 text-fg-muted">
            {t("credPerUser_a")} <code className="font-mono text-fg">{server.credential_env}</code> {t("credPerUser_b")}
          </p>
          <ConnectionEnvForm key={server.slug} server={server} onSaved={onChanged} />
        </>
      ) : server.auth_kind === "header" ? (
        <HeaderConfigForm key={server.slug} server={server} onSaved={onChanged} />
      ) : (
        <OAuthClientForm server={server} onSaved={onChanged} />
      )}
      <SharedConnection slug={server.slug} authKind={server.auth_kind} exists={server.has_shared_connection} shared={shared} header={server.header} onChanged={changed} />
    </div>
  );
}
