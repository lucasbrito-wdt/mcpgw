// Ported from design/v5/src/app/layout.tsx on 2026-10-31.
import type { Metadata } from "next";
import type { ReactNode } from "react";

import { DEFAULT_LOCALE, I18nProvider } from "../lib/i18n";
import { meta } from "../lib/i18n/messages/meta";
import { ToastProvider } from "../components/ui/toast";
import { geistMono, instrumentSans, martianMono } from "./fonts";
import { NebulaBackdrop } from "./nebula-backdrop";
import "./globals.css";

export const metadata: Metadata = {
  title: { default: "mcpgw", template: "%s | mcpgw" },
  description: meta.en.description,
};

export default function RootLayout({ children }: Readonly<{ children: ReactNode }>) {
  return (
    <html lang={DEFAULT_LOCALE} className={`dark ${instrumentSans.variable} ${martianMono.variable} ${geistMono.variable}`}>
      <body>
        <NebulaBackdrop />
        <I18nProvider>
          <ToastProvider>{children}</ToastProvider>
        </I18nProvider>
      </body>
    </html>
  );
}
