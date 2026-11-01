// Shapes of `GET /executions` and `GET /executions/{id}` (plan wave-4a, Task 4.1). Text fields are untrusted.
export type ExecutionStatus = "running" | "succeeded" | "failed" | "timed_out" | "cancelled";

export interface Execution {
  id: string;
  user_id: string;
  email: string;
  client_id: string;
  kind: string;
  status: ExecutionStatus | string;
  code: string;
  code_truncated: boolean;
  inner_calls: number;
  error?: string | null;
  started_at: string;
  finished_at?: string | null;
}

export interface ExecutionAuditEvent {
  ts: string;
  server: string;
  tool: string;
  decision: "allow" | "deny" | string;
  reason: string;
  latency_ms?: number | null;
  result_bytes?: number | null;
}

export interface ExecutionDetail {
  execution: Execution;
  audit_events: ExecutionAuditEvent[];
}
