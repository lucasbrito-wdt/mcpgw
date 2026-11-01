"use client";

import { useSyncExternalStore } from "react";

import { ServerDetailScreen } from "../../../../components/admin-servers/server-detail";

const noop = () => () => {};
const readSlug = () => new URLSearchParams(window.location.search).get("slug");

/** `?slug=`: static export has no dynamic segments, and the query is only known in the browser. */
export default function AdminServerViewPage() {
  const slug = useSyncExternalStore<string | null | undefined>(noop, readSlug, () => undefined);
  return <ServerDetailScreen slug={slug} />;
}
