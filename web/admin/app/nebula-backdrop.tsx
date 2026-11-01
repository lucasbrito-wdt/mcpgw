// Ported from design/v5/src/app/nebula-backdrop.tsx on 2026-10-31.
"use client";

import { usePathname } from "next/navigation";

import { NebulaField } from "../components/signature/NebulaField";

/** One nebula for the whole app: full on the portal and the index, about a third on the admin. */
export function NebulaBackdrop() {
  const pathname = usePathname();
  return <NebulaField intensity={pathname.startsWith("/admin") ? 0.35 : 1} />;
}
