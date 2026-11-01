"use client";

import { useSearchParams } from "next/navigation";

import { isValidProfile } from "../../lib/portal-types";

import { ServerDetailScreen } from "./server-detail-screen";

/** /portal/connect/?slug=<slug>&profile=<name>: static export has no dynamic segment, so both ride the query string. */
export function ConnectScreen() {
  const params = useSearchParams();
  const profile = params.get("profile") ?? "";
  return <ServerDetailScreen slug={params.get("slug")} initialProfile={isValidProfile(profile) ? profile : undefined} />;
}
