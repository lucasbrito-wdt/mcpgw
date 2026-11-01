"use client";

import { Plug, Power, RefreshCw } from "lucide-react";
import { useState, type ReactNode } from "react";

import { serverStatusLabel, type ServerDetail, type SyncReport } from "../../lib/admin-types";
import { api, ApiError } from "../../lib/api";
import { formatDateTime } from "../../lib/format";
import { useT } from "../../lib/i18n";
import { servers } from "../../lib/i18n/messages/servers";
import type { ProbeResult } from "../../lib/stats-types";
import { Button } from "../ui/button";
import { useToast } from "../ui/toast";

import { formatProbe, type ProbeMessage } from "./probe-format";
import { ProbeLine } from "./probe-line";
import { StdioEnvForm } from "./stdio-env-form";

const REPORT_KEYS = ["added", "changed", "removed", "quarantined"] as const satisfies readonly (keyof SyncReport)[];

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-fg-muted">{label}</dt>
      <dd className="min-w-0 break-all text-fg">{children}</dd>
    </>
  );
}

function Report({ report }: { report: SyncReport }) {
  const t = useT(servers);
  return (
    <div role="status" className="rounded-card border border-line bg-ink/50 p-4 text-[13px]">
      <p className="font-semibold text-fg">{t("syncDone")}</p>
      <ul className="mt-2 flex flex-col gap-1 text-fg-muted">
        {REPORT_KEYS.map((key) => (
          <li key={key}>
            <span className="font-mono text-fg tabular-nums">{report[key].length}</span> {t(`report_${key}`)}
            {report[key].length > 0 ? <span className="font-mono text-xs">: {report[key].join(", ")}</span> : null}
          </li>
        ))}
      </ul>
    </div>
  );
}

export interface DetailConnectionProps {
  server: ServerDetail;
  onChanged: () => void;
}

/** Where the gateway reaches the server, when its catalog was last read, and the sync and enable/disable actions. */
export function DetailConnection({ server, onChanged }: DetailConnectionProps) {
  const t = useT(servers);
  const toast = useToast();
  const [syncing, setSyncing] = useState(false);
  const [toggling, setToggling] = useState(false);
  const [report, setReport] = useState<SyncReport | null>(null);
  const [syncError, setSyncError] = useState<string | null>(null);
  const [testing, setTesting] = useState(false);
  const [probe, setProbe] = useState<ProbeMessage | null>(null);
  const disabled = server.status === "disabled";

  async function testConnection() {
    setTesting(true);
    setProbe(null);
    try {
      setProbe(formatProbe(await api<ProbeResult>("admin", `/servers/${encodeURIComponent(server.slug)}/test`, { method: "POST" }), "server"));
    } catch (e) {
      setProbe({ tone: "cut", text: e instanceof ApiError ? e.message : t("testFailed") });
    } finally {
      setTesting(false);
    }
  }

  async function sync() {
    setSyncing(true);
    setSyncError(null);
    try {
      setReport(await api<SyncReport>("admin", `/servers/${encodeURIComponent(server.slug)}/sync`, { method: "POST" }));
      onChanged();
    } catch (e) {
      setReport(null);
      setSyncError(e instanceof ApiError ? e.message : t("syncFailed"));
    } finally {
      setSyncing(false);
    }
  }

  async function toggle() {
    setToggling(true);
    try {
      await api("admin", `/servers/${encodeURIComponent(server.slug)}`, { method: "PATCH", body: { status: disabled ? "active" : "disabled" } });
      toast(disabled ? t("enabled") : t("disabled"), { tone: "ok" });
      onChanged();
    } catch (e) {
      toast(e instanceof ApiError ? e.message : t("toggleFailed"), { tone: "cut" });
    } finally {
      setToggling(false);
    }
  }

  return (
    <div className="flex flex-col gap-5">
      <dl className="grid gap-x-6 gap-y-2 rounded-card border border-line bg-panel/70 p-4 text-[13px] sm:grid-cols-[12rem_1fr]">
        <Row label={t("row_status")}>{serverStatusLabel(server.status)}</Row>
        {server.stdio_launcher ? (
          <Row label={t("row_dockerImage")}>
            <span className="font-mono">{server.docker_image}</span>
          </Row>
        ) : (
          <Row label={t("row_address")}>
            <span className="font-mono">{server.url}</span>
          </Row>
        )}
        {server.stdio_launcher ? (
          <>
            <Row label={t("row_network")}>{server.stdio_network ? t("networkOn") : t("networkOff")}</Row>
            <Row label={t("row_env")}>
              {server.stdio_env_names?.length ? <span className="font-mono">{server.stdio_env_names.join(", ")}</span> : t("none")}
              {server.stdio_env_names?.length ? <span className="text-fg-muted">{t("valuesInVault")}</span> : null}
            </Row>
            <Row label={t("row_credential")}>
              {server.credential_env ? (
                <>
                  {t("variable")} <span className="font-mono">{server.credential_env}</span>
                </>
              ) : (
                t("none")
              )}
            </Row>
          </>
        ) : null}
        <Row label={t("row_transport")}>
          <span className="font-mono">{server.detected_transport || server.transport}</span>
          {server.detected_protocol ? <span className="text-fg-muted">{t("protocolSuffix", { protocol: server.detected_protocol })}</span> : null}
        </Row>
        <Row label={t("row_synced")}>{server.catalog_synced_at ? formatDateTime(server.catalog_synced_at) : t("neverSynced")}</Row>
        <Row label={t("row_autoApprove")}>{server.auto_approve_new_tools ? t("yes") : t("noGoesToReview")}</Row>
        <Row label={t("row_trust")}>{server.trust_annotations ? t("yes") : t("no")}</Row>
      </dl>

      {server.stdio_launcher ? <StdioEnvForm key={server.slug} server={server} onSaved={onChanged} /> : null}

      <div className="flex flex-wrap items-center gap-2">
        <Button variant="primary" loading={syncing} onClick={() => void sync()}>
          <RefreshCw aria-hidden />
          {t("syncCatalog")}
        </Button>
        <Button variant="secondary" loading={testing} onClick={() => void testConnection()}>
          <Plug aria-hidden />
          {t("testConnection")}
        </Button>
        <Button variant={disabled ? "secondary" : "danger"} loading={toggling} onClick={() => void toggle()}>
          <Power aria-hidden />
          {disabled ? t("enable") : t("disable")}
        </Button>
      </div>

      {probe ? <ProbeLine message={probe} /> : null}
      {syncError ? (
        <p role="alert" className="rounded-card bg-cut-wash p-3 text-[13px] text-cut ring-1 ring-cut/35 ring-inset">
          {syncError}
        </p>
      ) : null}
      {report ? <Report report={report} /> : null}
    </div>
  );
}
