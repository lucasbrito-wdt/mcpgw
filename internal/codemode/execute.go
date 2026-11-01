package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lucasbrito-wdt/mcpgw/internal/audit"
	"github.com/lucasbrito-wdt/mcpgw/internal/catalog"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
	"github.com/lucasbrito-wdt/mcpgw/internal/sandbox"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
	"github.com/lucasbrito-wdt/mcpgw/internal/upstreamauth"
)

const (
	maxUpstreamErrorChars = 300
	maxLoggedNameChars    = 128
)

// Extra audit reasons for calls that policy allowed but the gateway did not forward.
const (
	reasonNotConnected  = "not_connected"
	reasonCredential    = "credential_error"
	reasonUpstreamError = "upstream_error"
	reasonAmbiguous     = "ambiguous_tool"
	reasonBadProfile    = "invalid_profile"
)

// Execute runs code (an async arrow function, see sandbox) for subj and returns its JSON result.
// Errors wrapping a sandbox error (ErrScript, ErrTimeout, ErrForbiddenSyntax, ...) are caused by
// the script and their messages are safe for the model; errors wrapping ErrInternal are gateway
// failures. The execution and every inner call are audited.
func (s *Service) Execute(ctx context.Context, subj policy.Subject, code string) (json.RawMessage, error) {
	release, ok := s.acquireSubject(subj.UserID)
	if !ok {
		s.log.Warn("codemode: too many concurrent executions", "user", subj.UserID, "max", s.maxPerSubject)
		return nil, fmt.Errorf("%w: too many scripts of yours are already running; wait for them to finish and retry", sandbox.ErrBusy)
	}
	defer release()
	execID, err := s.audit.StartExecution(ctx, audit.Execution{
		UserID: subj.UserID, ClientID: subj.ClientID, Kind: audit.KindExecute, Code: code,
	})
	if err != nil {
		s.log.Error("codemode: start execution audit", "error", err)
		return nil, fmt.Errorf("%w: audit unavailable", ErrInternal)
	}
	run := &execution{svc: s, subj: subj, id: execID}
	lim := s.limits
	// Engines that pool processes (montyrs) never hand one user's worker to another.
	lim.Owner = subj.UserID
	out, err := s.sandbox.Run(ctx, code, run.bridge, lim)

	status, msg := executionStatus(err)
	if ferr := s.audit.FinishExecution(ctx, execID, status, int(run.calls.Load()), msg); ferr != nil {
		s.log.Error("codemode: finish execution audit", "execution", execID, "error", ferr)
	}
	return out, err
}

func executionStatus(err error) (status, msg string) {
	switch {
	case err == nil:
		return audit.StatusSucceeded, ""
	case errors.Is(err, sandbox.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return audit.StatusTimedOut, err.Error()
	case errors.Is(err, context.Canceled):
		return audit.StatusCancelled, err.Error()
	default:
		return audit.StatusFailed, err.Error()
	}
}

// execution is the state of one Execute call: the sandbox bridge belongs to it.
type execution struct {
	svc   *Service
	subj  policy.Subject
	id    string
	calls atomic.Int64
}

// bridge handles one tools.call from the script. Each invocation records exactly one audit event.
func (x *execution) bridge(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	x.calls.Add(1)
	start := time.Now()
	s := x.svc

	server, tool, ok := catalog.ParseQualified(name)
	if !ok {
		x.deny("", truncateRunes(name, maxLoggedNameChars), policy.ReasonUnknownTool, args, start, "")
		return nil, denialError(policy.ReasonUnknownTool, "")
	}
	snap, err := s.catalog.get(ctx)
	if err != nil {
		s.log.Error("codemode: catalog", "error", err)
		x.deny(server, tool, policy.ReasonPolicyError, args, start, "")
		return nil, denialError(policy.ReasonPolicyError, server)
	}
	if snap.isInactive(server, tool) { // never fall through to a similarly named active tool
		x.deny(server, truncateRunes(tool, maxLoggedNameChars), policy.ReasonToolQuarantined, args, start, "")
		return nil, denialError(policy.ReasonToolQuarantined, server)
	}
	tool, ok = snap.resolveTool(server, tool)
	if !ok {
		x.deny(server, truncateRunes(tool, maxLoggedNameChars), reasonAmbiguous, args, start, "")
		return nil, denialError(reasonAmbiguous, server)
	}
	// Only a tool the snapshot knows was declared with a profile argument; a tool missing from it
	// (stale snapshot) is forwarded with its arguments untouched and the default connection.
	args, profile, err := takeProfile(args, snap.byQualified[catalog.QualifiedName(server, tool)].ProfileArg)
	if err != nil {
		x.deny(server, tool, reasonBadProfile, args, start, "")
		return nil, err
	}

	d := s.policy.Decide(ctx, x.subj, server, tool)
	if !d.Allow {
		x.deny(server, tool, d.Reason, args, start, profile)
		return nil, denialError(d.Reason, server)
	}
	srv, ok := snap.servers[server]
	if !ok { // policy knows a server the cached catalog does not: reload once
		if snap, err = s.catalog.refresh(ctx); err == nil {
			srv, ok = snap.servers[server]
		}
	}
	if !ok {
		x.deny(server, tool, policy.ReasonUnknownTool, args, start, profile)
		return nil, denialError(policy.ReasonUnknownTool, server)
	}

	cred, err := s.credential(ctx, srv, x.subj.UserID, profile)
	if err != nil {
		reason := reasonCredential
		if isNotConnected(err) {
			reason = reasonNotConnected
		} else {
			s.log.Error("codemode: credential", "server", server, "error", err)
		}
		x.deny(server, tool, reason, args, start, profile)
		if reason == reasonNotConnected && profile != "" && profile != upstreamauth.DefaultProfile {
			return nil, s.profileNotConnectedError(ctx, srv, server, x.subj.UserID, profile, err)
		}
		if reason == reasonNotConnected && profile == upstreamauth.DefaultProfile {
			if e := s.missingDefaultError(ctx, srv, server, x.subj.UserID); e != nil {
				return nil, e
			}
		}
		return nil, s.credentialError(reason, server)
	}

	if d.Required >= policy.LevelWrite {
		// Sync before forwarding: a write must be on record even if the process dies mid-call.
		// The outcome follows as a second event below.
		if err := x.record(ctx, server, tool, profile, audit.DecisionAllow, d.Reason, args, start, 0, audit.Sync); err != nil {
			s.log.Error("codemode: audit write", "error", err)
			return nil, errors.New("audit unavailable: call not forwarded")
		}
	}

	// Pool sessions are keyed by owner too: unauthenticated and stdio servers must not share a session across users.
	out, err := s.forward(upstream.WithOwner(ctx, x.subj.UserID), srv, cred, tool, args)
	reason := d.Reason
	if err != nil {
		reason = reasonUpstreamError
	}
	_ = x.record(ctx, server, tool, profile, audit.DecisionAllow, reason, args, start, len(out), audit.Async)
	return out, err
}

func (s *Service) credential(ctx context.Context, srv serverInfo, userID, profile string) (upstream.Credential, error) {
	if srv.authKind == "none" {
		return nil, nil
	}
	if s.creds == nil {
		return nil, ErrNotConnected
	}
	return s.creds.ProfileCredentialFor(ctx, srv.id, userID, profile)
}

// takeProfile removes the injected profile argument argName from args, so that policy, audit and
// the upstream never see it, and validates it. The returned profile is "" when argName is "" (no
// credentials: the audit stores NULL) and "default" when the call omits it; the audit records
// that requested name, also when "default" resolves to the shared connection. On error the
// stripped args are still returned, for the audit.
func takeProfile(args json.RawMessage, argName string) (json.RawMessage, string, error) {
	if argName == "" {
		return args, "", nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(args, &fields) != nil { // not an object: nothing to take, the upstream judges it
		return args, upstreamauth.DefaultProfile, nil
	}
	value, ok := fields[argName]
	if !ok {
		return args, upstreamauth.DefaultProfile, nil
	}
	delete(fields, argName)
	rest, err := json.Marshal(fields)
	if err != nil {
		return nil, "", fmt.Errorf("%w: re-encode arguments", ErrInternal)
	}
	var profile string
	if json.Unmarshal(value, &profile) != nil || !upstreamauth.ValidProfile(profile) {
		return rest, "", fmt.Errorf("invalid %s argument: use the name of a connection (lowercase letters, digits, _ or -, up to 32 characters, starting with a letter) or omit it to use default", argName)
	}
	return rest, profile, nil
}

// forward calls the upstream and converts its outcome into what the script may see.
func (s *Service) forward(ctx context.Context, srv serverInfo, cred upstream.Credential, tool string, args json.RawMessage) (json.RawMessage, error) {
	res, err := s.upstream.CallTool(ctx, srv.url, cred, tool, args)
	secrets := credentialSecrets(cred)
	if err != nil {
		return nil, upstreamError(err, secrets)
	}
	return scrub(res.Value, secrets), nil
}

func upstreamError(err error, secrets [][]byte) error {
	var te *upstream.ToolError
	switch {
	case errors.As(err, &te):
		return fmt.Errorf("upstream tool error: %s", clip(te.Message, secrets))
	case errors.Is(err, upstream.ErrInputRequired):
		return errors.New("upstream tool needs interactive input, which is not supported")
	case errors.Is(err, upstream.ErrResponseTooLarge):
		return errors.New("upstream response too large")
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("upstream call timed out")
	case errors.Is(err, context.Canceled):
		return errors.New("upstream call cancelled")
	default:
		return fmt.Errorf("upstream call failed: %s", clip(upstream.RedactURLs(err), secrets))
	}
}

func clip(msg string, secrets [][]byte) string {
	return truncateRunes(scrubString(msg, secrets), maxUpstreamErrorChars)
}

// deny records a denied call asynchronously.
func (x *execution) deny(server, tool, reason string, args json.RawMessage, start time.Time, profile string) {
	_ = x.record(context.Background(), server, tool, profile, audit.DecisionDeny, reason, args, start, 0, audit.Async)
}

func (x *execution) record(ctx context.Context, server, tool, profile, decision, reason string, args json.RawMessage, start time.Time, resultBytes int, mode audit.Mode) error {
	err := x.svc.audit.Record(ctx, audit.Event{
		ExecutionID: x.id, UserID: x.subj.UserID, ClientID: x.subj.ClientID,
		ServerSlug: truncateRunes(server, maxLoggedNameChars), ToolName: truncateRunes(tool, maxLoggedNameChars), Profile: profile, Decision: decision, Reason: reason, Args: args,
		LatencyMS: int(time.Since(start).Milliseconds()), ResultBytes: resultBytes,
	}, mode)
	if err != nil && mode == audit.Async {
		x.svc.log.Error("codemode: audit write", "error", err)
	}
	return err
}

// denialError builds the JS-visible error for a denied call.
func denialError(reason, server string) error {
	switch reason {
	case policy.ReasonNoConsent, policy.ReasonLevelExceeded, policy.ReasonConsentExpired:
		return fmt.Errorf("denied: %s: ask the user to raise the consent level for %s by reconnecting the client", reason, server)
	case policy.ReasonToolQuarantined:
		return fmt.Errorf("denied: %s: this tool is quarantined, waiting for admin approval of a changed definition, or disabled, and cannot be called", reason)
	case policy.ReasonNoCeiling:
		return fmt.Errorf("denied: %s: the user has no access to %s; an admin must grant it", reason, server)
	default:
		return fmt.Errorf("denied: %s", reason)
	}
}

func (s *Service) credentialError(reason, server string) error {
	if reason == reasonNotConnected {
		return fmt.Errorf("not connected: ask the user to open %s/connect/%s", s.publicURL, server)
	}
	return errors.New("upstream credential unavailable")
}

// missingDefaultError points a call without profile at the user's named connections; nil when
// there are none (or they cannot be listed), so the plain not-connected message applies.
func (s *Service) missingDefaultError(ctx context.Context, srv serverInfo, server, userID string) error {
	names, err := s.creds.Profiles(ctx, srv.id, userID)
	if err != nil || len(names) == 0 {
		return nil
	}
	return fmt.Errorf("denied: %s: no default connection on server %s; available connections: %s; pass profile with one of these names",
		reasonNotConnected, server, strings.Join(names, ", "))
}

// profileNotConnectedError tells the script which of the user's connections exist and where to
// connect the missing one.
func (s *Service) profileNotConnectedError(ctx context.Context, srv serverInfo, server, userID, profile string, err error) error {
	connectURL := s.publicURL + "/connect/" + server
	var nc *upstreamauth.NotConnectedError
	if errors.As(err, &nc) && nc.ConnectURL != "" {
		connectURL = nc.ConnectURL
	}
	connected := "none"
	if names, lerr := s.creds.Profiles(ctx, srv.id, userID); lerr != nil {
		s.log.Error("codemode: list connection profiles", "server", server, "error", lerr)
		connected = "unavailable"
	} else if len(names) > 0 {
		connected = strings.Join(names, ", ")
	}
	return fmt.Errorf("denied: %s: connection %q is not connected to server %s; available connections: %s; ask the user to connect at %s",
		reasonNotConnected, profile, server, connected, connectURL)
}

// isNotConnected implements the CredentialSource contract.
func isNotConnected(err error) bool {
	if errors.Is(err, ErrNotConnected) {
		return true
	}
	var nc interface{ NotConnected() bool }
	return errors.As(err, &nc) && nc.NotConnected()
}
