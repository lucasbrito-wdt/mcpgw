"use client";

import Link from "next/link";

import { useT } from "../lib/i18n";
import { notFound } from "../lib/i18n/messages/notFound";

// Replaces Next's default 404, which renders inline style attributes (blocked by the CSP, plan gap G26).
export default function NotFound() {
  const t = useT(notFound);
  return (
    <main className="grid min-h-dvh place-items-center px-4">
      <div className="max-w-sm text-center">
        <h1 className="text-2xl font-semibold tracking-[-0.03em] text-fg">{t("title")}</h1>
        <p className="mt-2 text-sm leading-6 text-fg-muted">{t("body")}</p>
        <Link href="/portal/" className="focus-ring mt-6 inline-block rounded-control text-sm font-medium text-signal-fg underline underline-offset-4">
          {t("goToPortal")}
        </Link>
      </div>
    </main>
  );
}
