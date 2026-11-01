"use client";

import { useCallback, useEffect, useState } from "react";

import { ApiError, api } from "../../lib/api";
import type { ConnectionsPage, ConnectionsSummary, Me, PortalApp, PortalAppDetail } from "../../lib/portal-types";

const PAGE_SIZE = 50;

export type AppSort = "name" | "last_used";

export interface AppsQuery {
  q: string;
  category: string;
  auth: string;
  sort: AppSort;
}

interface AppsState {
  key: string;
  items: PortalApp[];
  summary: ConnectionsSummary;
  nextCursor: string | null;
  failed: boolean;
  loadingMore: boolean;
}

function queryString(query: AppsQuery, cursor?: string): string {
  const params = new URLSearchParams({ sort: query.sort, limit: String(PAGE_SIZE) });
  if (query.q.trim()) params.set("q", query.q.trim());
  if (query.category) params.set("category", query.category);
  if (query.auth) params.set("auth", query.auth);
  if (cursor) params.set("cursor", cursor);
  return params.toString();
}

/** Value that follows `value` after `delay` ms without changes. */
export function useDebouncedValue<T>(value: T, delay: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(value), delay);
    return () => window.clearTimeout(timer);
  }, [value, delay]);
  return debounced;
}

/** `GET /me` of the portal: who is signed in and whether "Everyone" may be offered. */
export function usePortalMe(): Me | null {
  const [me, setMe] = useState<Me | null>(null);
  useEffect(() => {
    let cancelled = false;
    api<Me>("portal", "/me")
      .then((result) => {
        if (!cancelled) setMe(result);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);
  return me;
}

/**
 * Filtered, keyset-paginated app list. A query change (or `reload`) starts over from page 1; "loading" is
 * derived from the key, so no state is set synchronously inside the effect.
 */
export function usePortalApps(query: AppsQuery) {
  const [tick, setTick] = useState(0);
  const [state, setState] = useState<AppsState | null>(null);
  const key = `${queryString(query)}#${tick}`;

  useEffect(() => {
    let cancelled = false;
    api<ConnectionsPage>("portal", `/connections?${queryString(query)}`)
      .then((page) => {
        if (cancelled) return;
        setState({ key, items: page.items, summary: page.summary, nextCursor: page.next_cursor || null, failed: false, loadingMore: false });
      })
      .catch(() => {
        if (!cancelled) setState({ key, items: [], summary: {}, nextCursor: null, failed: true, loadingMore: false });
      });
    return () => {
      cancelled = true;
    };
    // `query` is covered by `key`.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  const current = state?.key === key ? state : null;

  const loadMore = useCallback(() => {
    if (!current?.nextCursor || current.loadingMore) return;
    const cursor = current.nextCursor;
    setState({ ...current, loadingMore: true });
    api<ConnectionsPage>("portal", `/connections?${queryString(query, cursor)}`)
      .then((page) => {
        setState((previous) =>
          previous?.key === key
            ? { ...previous, items: [...previous.items, ...page.items], summary: page.summary, nextCursor: page.next_cursor || null, loadingMore: false }
            : previous,
        );
      })
      .catch(() => {
        setState((previous) => (previous?.key === key ? { ...previous, loadingMore: false, failed: true } : previous));
      });
  }, [current, key, query]);

  const reload = useCallback(() => setTick((value) => value + 1), []);

  return {
    loading: current === null,
    failed: current?.failed ?? false,
    items: current?.items ?? [],
    summary: current?.summary ?? {},
    nextCursor: current?.nextCursor ?? null,
    loadingMore: current?.loadingMore ?? false,
    loadMore,
    reload,
  };
}

export type AppDetailState = { status: "loading" } | { status: "missing" } | { status: "failed" } | { status: "ready"; app: PortalAppDetail };

/** One app of the portal, with `reload` after a connect or disconnect. A 404 means "not yours or not there". */
export function usePortalApp(slug: string | null) {
  const [tick, setTick] = useState(0);
  const [result, setResult] = useState<{ key: string; state: AppDetailState } | null>(null);
  const key = `${slug ?? ""}#${tick}`;

  useEffect(() => {
    if (!slug) return;
    let cancelled = false;
    api<PortalAppDetail>("portal", `/connections/${encodeURIComponent(slug)}`)
      .then((app) => {
        if (!cancelled) setResult({ key, state: { status: "ready", app } });
      })
      .catch((error: unknown) => {
        if (!cancelled) setResult({ key, state: { status: error instanceof ApiError && error.status === 404 ? "missing" : "failed" } });
      });
    return () => {
      cancelled = true;
    };
    // `slug` is covered by `key`.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  const reload = useCallback(() => setTick((value) => value + 1), []);
  const state: AppDetailState = !slug ? { status: "missing" } : result?.key === key ? result.state : { status: "loading" };
  return { state, reload };
}
