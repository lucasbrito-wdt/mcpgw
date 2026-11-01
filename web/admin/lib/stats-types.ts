// Contract for GET /admin/api/stats, GET /admin/api/system and the server probes.
// Shared by the overview, logs, server detail, wizard and settings screens.

export type StatsWindow = "5m" | "15m" | "1h" | "24h" | "7d";

export const STATS_WINDOWS: readonly StatsWindow[] = ["5m", "15m", "1h", "24h", "7d"];

export interface StatsTotals {
  calls: number;
  allowed: number;
  denied: number;
  executions: number;
  failed_executions: number;
  p50_ms: number | null;
  p95_ms: number | null;
}

/** One zero-filled bucket; buckets are contiguous and oldest first. */
export interface StatsBucket {
  ts: string;
  calls: number;
  denied: number;
  p95_ms: number | null;
}

export interface ToolStat {
  server: string;
  tool: string;
  calls: number;
  denied: number;
  p95_ms: number | null;
}

export interface DenyStat {
  server: string;
  tool: string;
  reason: string;
  count: number;
}

export interface ServerStat {
  server: string;
  calls: number;
  denied: number;
  p95_ms: number | null;
}

export interface Stats {
  window: StatsWindow;
  from: string;
  to: string;
  bucket_seconds: number;
  totals: StatsTotals;
  series: StatsBucket[];
  top_tools: ToolStat[];
  top_denies: DenyStat[];
  servers: ServerStat[];
}

export interface SystemInfo {
  version: string;
  commit: string | null;
  commit_time: string | null;
  modified: boolean;
  go_version: string;
  public_url: string;
  started_at: string;
}

export interface ProbeResult {
  ok: boolean;
  latency_ms: number;
  status: number | null;
  requires_auth: boolean;
  tools: number | null;
  error: string | null;
}
