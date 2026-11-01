import type { Metadata } from "next";

import { LoginForm } from "../../../components/LoginForm";
import { meta } from "../../../lib/i18n/messages/meta";

// Static export: the title is the default locale; LoginForm re-sets document.title for the active locale.
export const metadata: Metadata = { title: meta.en.portalLoginTitle };

export default function PortalLoginPage() {
  return <LoginForm area="portal" />;
}
