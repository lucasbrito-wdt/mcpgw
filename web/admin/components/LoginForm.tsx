"use client";

import Link from "next/link";
import { useEffect, useId, useState, type FormEvent } from "react";

import { ApiError, api, type Area } from "../lib/api";
import { useT } from "../lib/i18n";
import { errors } from "../lib/i18n/messages/errors";
import { login } from "../lib/i18n/messages/login";
import type { SwitchboardServer } from "../lib/types";

import { SWITCHBOARD_AGENTS } from "./portal-v5/clients";
import { Switchboard } from "./signature/Switchboard";
import { Button } from "./ui/button";
import { resolveNextTarget } from "./login-target";
import { LocaleSwitcher } from "./shell/LocaleSwitcher";
import { Input } from "./ui/input";

export interface LoginFormProps {
  area: Area;
}

type LoginKey = keyof typeof login.en;

const ERROR_KEY_BY_STATUS: Record<number, LoginKey> = { 401: "invalidCredentials", 429: "tooManyAttempts" };

const AREA_LOGIN: Record<Area, { href: string; labelKey: LoginKey; titleKey: LoginKey; subtitleKey: LoginKey }> = {
  admin: { href: "/admin/login/", labelKey: "areaAdmin", titleKey: "adminTitle", subtitleKey: "adminSubtitle" },
  portal: { href: "/portal/login/", labelKey: "areaPortal", titleKey: "portalTitle", subtitleKey: "portalSubtitle" },
};

// The login page has no session to list real servers, so the board shows a fixed set of well-known logos.
const DEMO_SERVERS: readonly SwitchboardServer[] = [
  "github", "cloudflare", "postgres", "redis", "vercel",
  "netlify", "portainer", "kafka", "dokploy", "easypanel",
  "linear", "notion", "sentry", "stripe", "forgeboard",
].map((slug) => ({ slug, name: slug, key: "shared" }));

export function LoginForm({ area }: LoginFormProps) {
  const t = useT(login);
  const tErrors = useT(errors);
  const uid = useId();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  // Keep the message as a key so it re-translates when the locale changes.
  const [error, setError] = useState<LoginKey | "generic" | null>(null);
  const [pending, setPending] = useState(false);

  const { titleKey, subtitleKey } = AREA_LOGIN[area];
  useEffect(() => {
    document.title = `${t(titleKey)} | mcpgw`;
  }, [t, titleKey]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPending(true);
    setError(null);
    try {
      await api<void>(area, "/session", { method: "POST", body: { email, password } });
      window.location.assign(resolveNextTarget(new URLSearchParams(window.location.search).get("next"), area, window.location.origin));
    } catch (err) {
      setError(err instanceof ApiError ? (ERROR_KEY_BY_STATUS[err.status] ?? "generic") : "generic");
      setPending(false);
    }
  }

  return (
    <main className="grid min-h-dvh bg-black lg:grid-cols-[minmax(0,1fr)_minmax(24rem,30rem)]">
      <Switchboard
        className="order-last flex flex-col justify-center rounded-none border-0 lg:order-first lg:min-h-dvh"
        title="mcpgw"
        titleAs="h2"
        description={t("brandDescription")}
        servers={DEMO_SERVERS}
        agents={SWITCHBOARD_AGENTS}
        serversCaption={t("serversCaption")}
        maxTiles={15}
      />

      <section className="flex flex-col justify-center border-b border-line bg-ink px-6 py-12 sm:px-12 lg:min-h-dvh lg:border-b-0 lg:border-l">
        <div className="mx-auto w-full max-w-sm">
          <LocaleSwitcher className="mb-8" />
          <h1 className="text-3xl font-semibold tracking-[-0.03em] text-balance text-fg">{t(titleKey)}</h1>
          <p className="mt-2 text-sm text-fg-muted">{t(subtitleKey)}</p>
          <div className="mt-8">
      <form onSubmit={submit} className="flex w-full flex-col gap-4">
        <div className="flex flex-col gap-1.5">
          <label htmlFor={`${uid}-email`} className="text-[13px] font-medium text-fg">
            {t("email")}
          </label>
          <Input
            id={`${uid}-email`}
            type="email"
            name="email"
            autoComplete="username"
            placeholder={t("emailPlaceholder")}
            required
            value={email}
            onChange={(event) => setEmail(event.target.value)}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <label htmlFor={`${uid}-password`} className="text-[13px] font-medium text-fg">
            {t("password")}
          </label>
          <Input
            id={`${uid}-password`}
            type="password"
            name="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
        </div>
        <p role="alert" className="text-[13px] text-cut empty:hidden">
          {error === "generic" ? tErrors("generic") : error ? t(error) : null}
        </p>
        <Button type="submit" variant="primary" size="lg" loading={pending} className="w-full">
          {t("submit")}
        </Button>
        <nav aria-label={t("areaNav")} className="flex items-center gap-3 text-xs">
          {(Object.keys(AREA_LOGIN) as Area[]).map((key, index) => (
            <span key={key} className="flex items-center gap-3">
              {index > 0 ? <span aria-hidden className="h-3.5 w-px bg-line-strong" /> : null}
              <Link
                href={AREA_LOGIN[key].href}
                aria-current={key === area ? "page" : undefined}
                className="focus-ring rounded-control text-fg-subtle hover:text-fg aria-[current=page]:font-semibold aria-[current=page]:text-signal-fg"
              >
                {t(AREA_LOGIN[key].labelKey)}
              </Link>
            </span>
          ))}
        </nav>
      </form>
          </div>
        </div>
      </section>
    </main>
  );
}
