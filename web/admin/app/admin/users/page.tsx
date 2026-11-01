"use client";

import { Plus, Users } from "lucide-react";
import { useState, type FormEvent } from "react";

import { CELL, DataTable, ROW } from "../../../components/admin-logs/data-table";
import { Field, describedBy } from "../../../components/admin-servers/field";
import { LoadError, RowsSkeleton } from "../../../components/admin-servers/load-states";
import { ReauthCancelled, useReauth } from "../../../components/admin/ReauthDialog";
import { AppShell } from "../../../components/shell/AppShell";
import { PageHeader } from "../../../components/shell/PageHeader";
import { Badge } from "../../../components/ui/badge";
import { Button } from "../../../components/ui/button";
import { Dialog } from "../../../components/ui/dialog";
import { EmptyState } from "../../../components/ui/empty-state";
import { Input } from "../../../components/ui/input";
import { Toggle } from "../../../components/ui/toggle";
import { useToast } from "../../../components/ui/toast";
import { useLoad, type Paged } from "../../../lib/admin-types";
import { api, ApiError } from "../../../lib/api";
import { formatDateTime } from "../../../lib/format";
import { useT } from "../../../lib/i18n";
import { adminUsers } from "../../../lib/i18n/messages/adminUsers";

interface AdminUser {
  id: string;
  email: string;
  is_admin: boolean;
  created_at: string;
}

const MIN_PASSWORD = 12;
const errorText = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback);

function NewUserDialog({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (email: string) => void }) {
  const t = useT(adminUsers);
  const passwordHint = t("passwordHint", { min: MIN_PASSWORD });
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [isAdmin, setIsAdmin] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const reauth = useReauth();
  const [busy, setBusy] = useState(false);

  function close() {
    setEmail("");
    setPassword("");
    setIsAdmin(false);
    setErrors({});
    onClose();
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    const found: Record<string, string> = {};
    if (!email.trim()) found.email = t("emailRequired");
    if (password.length < MIN_PASSWORD) found.password = passwordHint;
    setErrors(found);
    if (Object.keys(found).length > 0) return;
    setBusy(true);
    try {
      await reauth.run(() => api("admin", "/users", { method: "POST", body: { email: email.trim(), password, is_admin: isAdmin } }));
      onCreated(email.trim());
      close();
    } catch (e) {
      if (e instanceof ReauthCancelled) return;
      setErrors(e instanceof ApiError && e.fields ? e.fields : { form: errorText(e, t("saveError")) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
    <Dialog
      open={open}
      onOpenChange={(next) => !next && !busy && close()}
      size="sm"
      title={t("newUser")}
      description={t("newDescription")}
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={close}>{t("cancel")}</Button>
          <Button variant="primary" type="submit" form="new-user-form" loading={busy}>{t("createUser")}</Button>
        </>
      }
    >
      <form id="new-user-form" noValidate onSubmit={submit} className="flex flex-col gap-4">
        <Field id="new-user-email" label={t("colEmail")} error={errors.email}>
          <Input id="new-user-email" type="email" autoComplete="off" value={email} onChange={(e) => setEmail(e.target.value)} disabled={busy} aria-invalid={Boolean(errors.email) || undefined} aria-describedby={describedBy("new-user-email", { error: errors.email })} />
        </Field>
        <Field id="new-user-password" label={t("initialPassword")} hint={passwordHint} error={errors.password}>
          <Input id="new-user-password" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} disabled={busy} aria-invalid={Boolean(errors.password) || undefined} aria-describedby={describedBy("new-user-password", { hint: passwordHint, error: errors.password })} />
        </Field>
        <div className="flex items-center gap-2.5">
          <Toggle checked={isAdmin} onCheckedChange={setIsAdmin} label={t("roleAdmin")} disabled={busy} />
          <span aria-hidden className="text-[13px] font-semibold text-fg">{t("roleAdmin")}</span>
        </div>
        {errors.form ? <p role="alert" className="text-xs text-cut">{errors.form}</p> : null}
      </form>
    </Dialog>
    {reauth.dialog}
    </>
  );
}

function AdminToggleDialog({ user, onClose, onDone }: { user: AdminUser | null; onClose: () => void; onDone: (message: string) => void }) {
  const t = useT(adminUsers);
  const reauth = useReauth();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const [last, setLast] = useState(user);
  if (user && user !== last) {
    setLast(user);
    setError(undefined);
  }
  const shown = user ?? last;
  const promote = shown ? !shown.is_admin : true;

  async function confirm() {
    if (!shown) return;
    setBusy(true);
    setError(undefined);
    try {
      await reauth.run(() => api("admin", `/users/${encodeURIComponent(shown.id)}`, { method: "PATCH", body: { is_admin: promote } }));
      onDone(t(promote ? "promotedToast" : "demotedToast", { email: shown.email }));
    } catch (e) {
      if (!(e instanceof ReauthCancelled)) setError(errorText(e, t("saveError")));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
    <Dialog
      open={user !== null}
      onOpenChange={(next) => !next && !busy && onClose()}
      size="sm"
      title={t(promote ? "promoteTitle" : "demoteTitle", { email: shown?.email ?? "" })}
      description={t(promote ? "promoteDescription" : "demoteDescription")}
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={onClose}>{t("cancel")}</Button>
          <Button variant={promote ? "primary" : "danger"} loading={busy} onClick={() => void confirm()}>{t(promote ? "promoteConfirm" : "demoteConfirm")}</Button>
        </>
      }
    >
      {error ? <p role="alert" className="text-xs text-cut">{error}</p> : null}
    </Dialog>
    {reauth.dialog}
    </>
  );
}

function PasswordDialog({ user, open, busy, error, onSubmit, onClose }: { user: AdminUser | null; open: boolean; busy: boolean; error?: string; onSubmit: (password: string) => void; onClose: () => void }) {
  const t = useT(adminUsers);
  const passwordHint = t("passwordHint", { min: MIN_PASSWORD });
  const [password, setPassword] = useState("");
  const [local, setLocal] = useState<string | undefined>();
  const shown = local ?? error;

  function submit(event: FormEvent) {
    event.preventDefault();
    if (password.length < MIN_PASSWORD) return setLocal(passwordHint);
    setLocal(undefined);
    onSubmit(password);
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => !next && !busy && onClose()}
      size="sm"
      title={t("resetPassword")}
      description={user ? t("resetDescription", { email: user.email }) : undefined}
      footer={
        <>
          <Button variant="ghost" disabled={busy} onClick={onClose}>{t("cancel")}</Button>
          <Button variant="primary" type="submit" form="reset-password-form" loading={busy}>{t("resetPassword")}</Button>
        </>
      }
    >
      <form id="reset-password-form" noValidate onSubmit={submit}>
        <Field id="reset-password" label={t("newPassword")} hint={passwordHint} error={shown}>
          <Input id="reset-password" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} disabled={busy} aria-invalid={Boolean(shown) || undefined} aria-describedby={describedBy("reset-password", { hint: passwordHint, error: shown })} />
        </Field>
      </form>
    </Dialog>
  );
}

export default function AdminUsersPage() {
  const t = useT(adminUsers);
  const toast = useToast();
  const users = useLoad<Paged<AdminUser>>("/users?limit=200");
  const reauth = useReauth();
  const [creating, setCreating] = useState(false);
  const [toggling, setToggling] = useState<AdminUser | null>(null);
  const [resetUser, setResetUser] = useState<AdminUser | null>(null);
  const [resetOpen, setResetOpen] = useState(false);
  const [resetBusy, setResetBusy] = useState(false);
  const [resetError, setResetError] = useState<string | undefined>();

  async function resetPassword(password: string) {
    if (!resetUser) return;
    // The password dialog steps aside so the reauth dialog is the only one on screen.
    setResetOpen(false);
    setResetBusy(true);
    setResetError(undefined);
    try {
      await reauth.run(() => api("admin", `/users/${encodeURIComponent(resetUser.id)}/password`, { method: "POST", body: { password } }));
      toast(t("toastReset", { email: resetUser.email }), { tone: "ok" });
      setResetUser(null);
    } catch (e) {
      if (!(e instanceof ReauthCancelled)) setResetError(errorText(e, t("saveError")));
      setResetOpen(true);
    } finally {
      setResetBusy(false);
    }
  }

  const items = users.data?.items ?? [];

  return (
    <AppShell surface="admin">
      <title>{t("pageTitle")}</title>
      <div className="flex flex-col gap-6">
        <PageHeader
          title={t("title")}
          description={t("description")}
          actions={
            <Button variant="primary" size="lg" onClick={() => setCreating(true)}>
              <Plus aria-hidden />
              {t("newUser")}
            </Button>
          }
        />
        {users.error ? <LoadError error={users.error} onRetry={users.reload} /> : null}
        {!users.error && !users.data ? <RowsSkeleton label={t("loading")} /> : null}
        {users.data && items.length === 0 ? <EmptyState icon={Users} title={t("emptyTitle")}>{t("emptyBody")}</EmptyState> : null}
        {items.length > 0 ? (
          <DataTable caption={t("caption")} columns={[{ label: t("colEmail") }, { label: t("colRole") }, { label: t("colCreated") }, { label: t("colActions"), hidden: true }]}>
            {items.map((user) => (
              <tr key={user.id} className={ROW}>
                <td className={`${CELL} font-semibold`}>{user.email}</td>
                <td className={CELL}>{user.is_admin ? <Badge tone="core">{t("roleAdmin")}</Badge> : <Badge>{t("roleUser")}</Badge>}</td>
                <td className={`${CELL} text-fg-muted`}>{formatDateTime(user.created_at)}</td>
                <td className={`${CELL} text-right`}>
                  <span className="flex flex-wrap justify-end gap-1">
                    <Button variant="ghost" size="sm" onClick={() => setToggling(user)} aria-label={t(user.is_admin ? "removeAdminAria" : "makeAdminAria", { email: user.email })}>
                      {t(user.is_admin ? "removeAdmin" : "makeAdmin")}
                    </Button>
                    <Button
                      variant="ghost"
                      size="sm"
                      aria-label={t("resetPasswordAria", { email: user.email })}
                      onClick={() => {
                        setResetError(undefined);
                        setResetUser(user);
                        setResetOpen(true);
                      }}
                    >
                      {t("resetPassword")}
                    </Button>
                  </span>
                </td>
              </tr>
            ))}
          </DataTable>
        ) : null}
        {users.data?.next_cursor ? <p className="text-xs text-fg-muted">{t("truncated")}</p> : null}
      </div>

      <NewUserDialog
        open={creating}
        onClose={() => setCreating(false)}
        onCreated={(email) => {
          toast(t("toastCreated", { email }), { tone: "ok" });
          users.reload();
        }}
      />
      <AdminToggleDialog
        user={toggling}
        onClose={() => setToggling(null)}
        onDone={(message) => {
          toast(message, { tone: "ok" });
          setToggling(null);
          users.reload();
        }}
      />
      <PasswordDialog key={resetUser?.id ?? "none"} user={resetUser} open={resetOpen} busy={resetBusy} error={resetError} onSubmit={(p) => void resetPassword(p)} onClose={() => { setResetOpen(false); setResetUser(null); }} />
      {reauth.dialog}
    </AppShell>
  );
}
