package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/lucasbrito-wdt/mcpgw/internal/store"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

// Lister is the part of upstream.Client that Sync needs.
type Lister interface {
	ListTools(ctx context.Context, serverURL string, cred upstream.Credential) ([]upstream.Tool, upstream.CacheHint, error)
}

// Server identifies an upstream server row plus the credential to list with.
type Server struct {
	ID         string // upstream_servers.id (uuid)
	Slug       string
	URL        string
	Credential upstream.Credential
	// KeepMissing leaves tools absent from this listing untouched instead of quarantining them.
	// For listings that are one user's view of the shared catalog (auto-sync after a connect),
	// which may be narrower than another user's.
	KeepMissing bool
	// InsertOnly restricts the sync to adding unknown tools as pending_approval: it never trusts
	// (no TOFU, auto_approve_new_tools is ignored), never rewrites or reclassifies an existing
	// tool and never stamps catalog_synced_at. Definitions that differ from the stored ones are
	// only reported (SyncReport.Ignored). For listings made with a plain user's credential.
	InsertOnly bool
}

// ValidToolName reports whether an upstream tool name is acceptable for the catalog.
func ValidToolName(name string) bool { return toolNamePattern.MatchString(name) }

var toolNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// Tool statuses (tools.status). Only active tools are visible to search and callable.
const (
	StatusActive          = "active"
	StatusQuarantined     = "quarantined"      // disappeared upstream
	StatusPendingApproval = "pending_approval" // new or changed definition waiting for `admin tool approve`
)

// Kinds of pending change (tools.pending_changes), shown by `admin tool list`.
const (
	ChangeNew         = "new"
	ChangeDescription = "description"
	ChangeSchema      = "schema" // input or output schema
	ChangeAnnotations = "annotations"
	ChangeDefinition  = "definition" // hash differs but the stored columns do not tell which part
)

// Change records a tool whose definition changed upstream.
type Change struct {
	Name    string
	OldHash string
	NewHash string
	Kinds   []string // which parts changed since the last approval (sorted)
}

// SyncReport summarizes one catalog sync.
type SyncReport struct {
	FirstSync    bool     // trust on first use: every listed tool was approved as listed
	Added        []string // new rows: active on the first sync or with auto-approve, else pending
	Changed      []Change // definition changed upstream (pending unless it matches the approved one)
	Pending      []string // became pending approval in this sync
	Reactivated  []string // back to active: reappeared or reverted to the approved definition
	Reclassified []string // trust_level recomputed without a definition change
	Quarantined  []string // disappeared upstream
	Rejected     []string // names that fail ValidToolName: skipped, never stored
	Ignored      []string // InsertOnly: existing tools whose listed definition differs from the stored one
	Unchanged    int
	Hint         upstream.CacheHint
}

// Sync lists the upstream tools and reconciles the tools table for the server (tool pinning):
//   - the first sync of a server trusts every listed definition (TOFU) and makes it active;
//   - later, a listed definition whose schema_hash differs from the approved one becomes
//     pending_approval and is unavailable (hidden from search, denied on execute) until an admin
//     approves it; the model never sees the new definition before that, nor the old one, since the
//     upstream now behaves as the new one says. A definition that reverts to the approved hash is
//     active again;
//   - a tool that appears after the first sync is pending unless the server auto-approves new
//     tools;
//   - a tool absent from the listing is quarantined (unless server.KeepMissing);
//   - a tool whose name fails ValidToolName is skipped and reported (Rejected);
//   - with server.InsertOnly none of the above applies except insertion: see Server.InsertOnly.
//
// Status and trust_level changes fire the tools NOTIFY trigger, so running gateways see them.
// Concurrent syncs of one server serialize on its upstream_servers row.
func Sync(ctx context.Context, db store.DB, client Lister, server Server) (SyncReport, error) {
	var report SyncReport
	listed, hint, err := client.ListTools(ctx, server.URL, server.Credential)
	if err != nil {
		return report, fmt.Errorf("list upstream tools for %s: %w", server.Slug, err)
	}
	report.Hint = hint

	tx, err := db.Begin(ctx)
	if err != nil {
		return report, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	s := &syncer{tx: tx, server: server, report: &report}
	if s.trust, err = lockServer(ctx, tx, server.ID); err != nil {
		return report, err
	}
	if server.InsertOnly {
		s.trust = serverTrust{} // never TOFU, never auto-approve
	}
	report.FirstSync = s.trust.firstSync
	existing, err := loadExisting(ctx, tx, server.ID)
	if err != nil {
		return report, err
	}

	seen := map[string]bool{}
	for _, t := range listed {
		if seen[t.Name] {
			continue // first definition wins on duplicate names
		}
		if !ValidToolName(t.Name) {
			report.Rejected = append(report.Rejected, t.Name)
			continue
		}
		seen[t.Name] = true
		if err := s.reconcile(ctx, t, existing[t.Name]); err != nil {
			return report, err
		}
	}
	if !server.KeepMissing {
		if err := s.quarantineMissing(ctx, existing, seen); err != nil {
			return report, err
		}
	}
	if !server.InsertOnly {
		if _, err := tx.Exec(ctx, `UPDATE upstream_servers SET catalog_synced_at = now() WHERE id = $1`, server.ID); err != nil {
			return report, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return report, err
	}
	return report, nil
}

// serverTrust is the per-server catalog trust configuration.
type serverTrust struct {
	annotations bool // readOnlyHint may lower the level
	autoApprove bool // new tools after the first sync are active at once
	firstSync   bool // never synced: trust on first use
}

func lockServer(ctx context.Context, tx pgx.Tx, serverID string) (serverTrust, error) {
	var st serverTrust
	err := tx.QueryRow(ctx, `SELECT trust_annotations, auto_approve_new_tools, catalog_synced_at IS NULL
		FROM upstream_servers WHERE id = $1 FOR UPDATE`, serverID).Scan(&st.annotations, &st.autoApprove, &st.firstSync)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, fmt.Errorf("catalog sync: server %s not found", serverID)
	}
	return st, err
}

// toolRow is the stored state of a tool, as needed to reconcile it.
type toolRow struct {
	hash         string
	approvedHash *string
	status       string
	level        string
	overridden   bool
	changes      []string
	description  string
	input        string
	output       *string
	annotations  string
}

func loadExisting(ctx context.Context, tx pgx.Tx, serverID string) (map[string]*toolRow, error) {
	rows, err := tx.Query(ctx, `SELECT name, schema_hash, approved_hash, status, trust_level, trust_level_overridden,
			pending_changes, description, input_schema::text, output_schema::text, annotations::text
		FROM tools WHERE server_id = $1 FOR UPDATE`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]*toolRow{}
	for rows.Next() {
		var name string
		var r toolRow
		if err := rows.Scan(&name, &r.hash, &r.approvedHash, &r.status, &r.level, &r.overridden,
			&r.changes, &r.description, &r.input, &r.output, &r.annotations); err != nil {
			return nil, err
		}
		out[name] = &r
	}
	return out, rows.Err()
}

type syncer struct {
	tx     pgx.Tx
	server Server
	trust  serverTrust
	report *SyncReport
}

// listedTool is an upstream tool with everything derived from it.
type listedTool struct {
	upstream.Tool
	hash               string
	level              Level
	input, annotations string
	output             *string
}

func (s *syncer) reconcile(ctx context.Context, t upstream.Tool, row *toolRow) error {
	lt := listedTool{Tool: t, level: Classify(FromUpstream(s.server.Slug, t), s.trust.annotations)}
	var err error
	if lt.hash, err = SchemaHash(t); err != nil {
		return fmt.Errorf("hash tool %s: %w", t.Name, err)
	}
	if lt.input, lt.output, lt.annotations, err = jsonColumns(t); err != nil {
		return err
	}
	switch {
	case row == nil:
		return s.insert(ctx, lt)
	case s.server.InsertOnly:
		if row.hash != lt.hash {
			s.report.Ignored = append(s.report.Ignored, lt.Name)
		} else {
			s.report.Unchanged++
		}
		return nil
	case s.trust.firstSync || (row.approvedHash != nil && *row.approvedHash == lt.hash):
		return s.activate(ctx, lt, row)
	default:
		return s.holdForApproval(ctx, lt, row)
	}
}

func (s *syncer) insert(ctx context.Context, lt listedTool) error {
	status, approved, changes := StatusActive, &lt.hash, []string{}
	if !s.trust.firstSync && !s.trust.autoApprove {
		status, approved, changes = StatusPendingApproval, nil, []string{ChangeNew}
		s.report.Pending = append(s.report.Pending, lt.Name)
	}
	_, err := s.tx.Exec(ctx, `
		INSERT INTO tools (server_id, name, description, input_schema, output_schema, annotations, schema_hash,
			trust_level, status, approved_hash, pending_changes)
		VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, $6::jsonb, $7, $8, $9, $10, $11)`,
		s.server.ID, lt.Name, lt.Description, lt.input, lt.output, lt.annotations, lt.hash,
		string(lt.level), status, approved, changes)
	s.report.Added = append(s.report.Added, lt.Name)
	return err
}

// activate stores an approved definition (first sync, or equal to the approved hash) as active.
func (s *syncer) activate(ctx context.Context, lt listedTool, row *toolRow) error {
	if row.hash == lt.hash && row.status == StatusActive && (row.approvedHash != nil && *row.approvedHash == lt.hash) {
		return s.keep(ctx, lt, row)
	}
	if err := s.write(ctx, lt, StatusActive, &lt.hash, []string{}); err != nil {
		return err
	}
	if row.status != StatusActive {
		s.report.Reactivated = append(s.report.Reactivated, lt.Name)
	}
	s.recordChange(lt, row, changedParts(row, lt))
	return nil
}

// holdForApproval makes a definition that differs from the approved one pending approval.
func (s *syncer) holdForApproval(ctx context.Context, lt listedTool, row *toolRow) error {
	if row.hash == lt.hash && row.status == StatusPendingApproval {
		return s.keep(ctx, lt, row)
	}
	kinds := mergeKinds(row.changes, changedParts(row, lt))
	switch {
	case len(kinds) > 0:
	case row.approvedHash == nil:
		kinds = []string{ChangeNew}
	default:
		kinds = []string{ChangeDefinition}
	}
	if err := s.write(ctx, lt, StatusPendingApproval, row.approvedHash, kinds); err != nil {
		return err
	}
	if row.status != StatusPendingApproval {
		s.report.Pending = append(s.report.Pending, lt.Name)
	}
	s.recordChange(lt, row, kinds)
	return nil
}

func (s *syncer) recordChange(lt listedTool, row *toolRow, kinds []string) {
	if row.hash != lt.hash {
		s.report.Changed = append(s.report.Changed, Change{Name: lt.Name, OldHash: row.hash, NewHash: lt.hash, Kinds: kinds})
	}
}

// keep handles an unchanged definition; only the level may need recomputing (annotation trust
// toggled or heuristic changed). trust_level is never overwritten when an admin set it by hand.
func (s *syncer) keep(ctx context.Context, lt listedTool, row *toolRow) error {
	if row.overridden || row.level == string(lt.level) {
		s.report.Unchanged++
		return nil
	}
	if _, err := s.tx.Exec(ctx, `UPDATE tools SET trust_level = $3, updated_at = now() WHERE server_id = $1 AND name = $2`,
		s.server.ID, lt.Name, string(lt.level)); err != nil {
		return err
	}
	s.report.Reclassified = append(s.report.Reclassified, lt.Name)
	return nil
}

// write stores the listed definition with the given status, approved hash and pending changes.
func (s *syncer) write(ctx context.Context, lt listedTool, status string, approved *string, changes []string) error {
	_, err := s.tx.Exec(ctx, `
		UPDATE tools SET description = $3, input_schema = $4::jsonb, output_schema = $5::jsonb,
			annotations = $6::jsonb, schema_hash = $7, status = $9, approved_hash = $10, pending_changes = $11,
			updated_at = now(),
			trust_level = CASE WHEN trust_level_overridden THEN trust_level ELSE $8 END
		WHERE server_id = $1 AND name = $2`,
		s.server.ID, lt.Name, lt.Description, lt.input, lt.output, lt.annotations, lt.hash,
		string(lt.level), status, approved, changes)
	return err
}

func (s *syncer) quarantineMissing(ctx context.Context, existing map[string]*toolRow, seen map[string]bool) error {
	for name, row := range existing {
		if seen[name] || row.status == StatusQuarantined {
			continue
		}
		if _, err := s.tx.Exec(ctx,
			`UPDATE tools SET status = 'quarantined', updated_at = now() WHERE server_id = $1 AND name = $2`,
			s.server.ID, name); err != nil {
			return err
		}
		s.report.Quarantined = append(s.report.Quarantined, name)
	}
	sort.Strings(s.report.Quarantined)
	return nil
}

// changedParts compares the stored definition with the listed one. JSON is compared by value,
// since jsonb does not keep the upstream formatting.
func changedParts(row *toolRow, lt listedTool) []string {
	var kinds []string
	if row.description != lt.Description {
		kinds = append(kinds, ChangeDescription)
	}
	if !sameJSON(&row.input, &lt.input) || !sameJSON(row.output, lt.output) {
		kinds = append(kinds, ChangeSchema)
	}
	if !sameJSON(&row.annotations, &lt.annotations) {
		kinds = append(kinds, ChangeAnnotations)
	}
	return kinds
}

func sameJSON(a, b *string) bool {
	va, errA := decodeJSON(a)
	vb, errB := decodeJSON(b)
	return errA == nil && errB == nil && reflect.DeepEqual(va, vb)
}

func decodeJSON(s *string) (any, error) {
	if s == nil {
		return nil, nil
	}
	var v any
	err := json.Unmarshal([]byte(*s), &v)
	return v, err
}

// mergeKinds returns the sorted union of two change-kind lists.
func mergeKinds(a, b []string) []string {
	out := slices.Concat(a, b)
	slices.Sort(out)
	return slices.Compact(out)
}

func jsonColumns(t upstream.Tool) (input string, output *string, annotations string, err error) {
	input = "{}"
	if len(t.InputSchema) > 0 {
		input = string(t.InputSchema)
	}
	if len(t.OutputSchema) > 0 {
		s := string(t.OutputSchema)
		output = &s
	}
	a, err := json.Marshal(t.Annotations)
	return input, output, string(a), err
}

// SchemaHash is the hex sha256 of the canonical JSON of
// {name, description, input_schema, output_schema, annotations}.
// Canonical JSON: object keys sorted, no insignificant whitespace, no HTML escaping, numbers
// kept as their original literals; absent schemas are null.
func SchemaHash(t upstream.Tool) (string, error) {
	annotations, err := json.Marshal(t.Annotations)
	if err != nil {
		return "", err
	}
	doc := map[string]json.RawMessage{
		"name":          mustJSON(t.Name),
		"description":   mustJSON(t.Description),
		"input_schema":  rawOrNull(t.InputSchema),
		"output_schema": rawOrNull(t.OutputSchema),
		"annotations":   annotations,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	canon, err := Canonicalize(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), nil
}

// Canonicalize re-encodes JSON with sorted object keys and no insignificant whitespace.
func Canonicalize(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil { // map keys are sorted by encoding/json
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func rawOrNull(r json.RawMessage) json.RawMessage {
	if len(r) == 0 {
		return json.RawMessage("null")
	}
	return r
}

func mustJSON(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}
