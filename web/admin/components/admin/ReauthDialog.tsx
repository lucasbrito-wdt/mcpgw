"use client";

import { useRef, useState, type FormEvent, type ReactNode } from "react";

import { api, ApiError } from "../../lib/api";
import { useT } from "../../lib/i18n";
import { adminReauth } from "../../lib/i18n/messages/adminReauth";
import { Field, describedBy } from "../admin-servers/field";
import { Button } from "../ui/button";
import { Dialog } from "../ui/dialog";
import { Input } from "../ui/input";

export interface ReauthDialogProps {
  open: boolean;
  /** Called after `POST /session/reauth` succeeded. */
  onConfirmed: () => void;
  onCancel: () => void;
}

/** "Confirme sua senha para continuar": renews `reauth_at` (valid for 5 minutes) with the admin's password. */
export function ReauthDialog({ open, onConfirmed, onCancel }: ReauthDialogProps) {
  const t = useT(adminReauth);
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | undefined>();
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!password) return setError(t("required"));
    setBusy(true);
    setError(undefined);
    try {
      await api<void>("admin", "/session/reauth", { method: "POST", body: { password } });
      setPassword("");
      onConfirmed();
    } catch (e) {
      setError(e instanceof ApiError && e.status !== 401 ? e.message : t("wrong"));
    } finally {
      setBusy(false);
    }
  }

  function cancel() {
    setPassword("");
    setError(undefined);
    onCancel();
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => !next && !busy && cancel()}
      size="sm"
      title={t("title")}
      description={t("description")}
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={cancel}>
            {t("cancel")}
          </Button>
          <Button variant="primary" type="submit" form="reauth-form" loading={busy}>
            {t("confirm")}
          </Button>
        </>
      }
    >
      <form id="reauth-form" noValidate onSubmit={submit}>
        <Field id="reauth-password" label={t("password")} error={error}>
          <Input id="reauth-password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} disabled={busy} aria-invalid={Boolean(error) || undefined} aria-describedby={describedBy("reauth-password", { error })} />
        </Field>
      </form>
    </Dialog>
  );
}

/** The person closed the password dialog; the secret write did not happen. */
export class ReauthCancelled extends Error {
  constructor() {
    super("reauth cancelled");
    this.name = "ReauthCancelled";
  }
}

interface Waiter {
  resolve: () => void;
  reject: (reason: Error) => void;
}

/**
 * Runs a secret write; on `reauth_required` opens the password dialog, then retries once.
 * Render `dialog` somewhere in the screen. Rejects with `ReauthCancelled` when the dialog is dismissed.
 */
export function useReauth(): { run: <T>(action: () => Promise<T>) => Promise<T>; dialog: ReactNode } {
  const [open, setOpen] = useState(false);
  const waiter = useRef<Waiter | null>(null);

  const ask = () =>
    new Promise<void>((resolve, reject) => {
      waiter.current = { resolve, reject };
      setOpen(true);
    });

  async function run<T>(action: () => Promise<T>): Promise<T> {
    try {
      return await action();
    } catch (e) {
      if (!(e instanceof ApiError) || e.code !== "reauth_required") throw e;
    }
    await ask();
    return action();
  }

  const settle = (done: (w: Waiter) => void) => {
    setOpen(false);
    if (waiter.current) done(waiter.current);
    waiter.current = null;
  };

  const dialog = <ReauthDialog open={open} onConfirmed={() => settle((w) => w.resolve())} onCancel={() => settle((w) => w.reject(new ReauthCancelled()))} />;
  return { run, dialog };
}
