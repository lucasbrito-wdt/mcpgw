package admin

import "context"

// Overview is the dashboard summary; every number comes from one query.
type Overview struct {
	Servers, ServersDisabled      int
	ToolsActive, ToolsQuarantined int
	PendingApprovals              int // tools in status pending_approval
	Denies24h, Executions24h      int
}

func (s *Service) Overview(ctx context.Context) (o Overview, err error) {
	// audit_events is partitioned on ts: the predicate keeps the scan on recent partitions.
	err = s.o.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM upstream_servers WHERE status = 'active'),
		(SELECT count(*) FROM upstream_servers WHERE status = 'disabled'),
		(SELECT count(*) FROM tools WHERE status = 'active'),
		(SELECT count(*) FROM tools WHERE status = 'quarantined'),
		(SELECT count(*) FROM tools WHERE status = 'pending_approval'),
		(SELECT count(*) FROM audit_events WHERE decision = 'deny' AND ts > now() - interval '24 hours'),
		(SELECT count(*) FROM executions WHERE started_at > now() - interval '24 hours')`).
		Scan(&o.Servers, &o.ServersDisabled, &o.ToolsActive, &o.ToolsQuarantined, &o.PendingApprovals, &o.Denies24h, &o.Executions24h)
	return o, err
}
