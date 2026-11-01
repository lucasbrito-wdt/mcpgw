// Shapes of `GET /connections` (plan wave-4a, Task 4.1). Emails and account labels are untrusted text.
import type { SharedConnection } from "../../lib/admin-types";

export interface ConnectionServer {
  slug: string;
  auth_kind: string;
  category: string;
  connected: number;
  using_shared: number;
  expiring: number;
  expired: number;
  refresh_failed: number;
  revoked: number;
  not_connected: number;
  shared: SharedConnection | null;
}

/** Status of one person on one server: connected, expiring, expired, refresh_failed, revoked_by_admin, shared or not_connected. */
export interface UserConnection {
  server: string;
  user_id: string;
  /** Connection name; "default" unless the person named it. */
  profile: string;
  email: string;
  status: string;
  account_label: string | null;
  last_used_at: string | null;
  expires_at: string | null;
}

export interface ConnectionsResponse {
  servers: ConnectionServer[];
  items: UserConnection[];
  next_cursor: string;
}
