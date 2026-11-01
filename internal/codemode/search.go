package codemode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/lucasbrito-wdt/mcpgw/internal/audit"
	"github.com/lucasbrito-wdt/mcpgw/internal/catalog"
	"github.com/lucasbrito-wdt/mcpgw/internal/policy"
)

const (
	maxSearchResults = 10
	maxQueryChars    = 500
)

// SearchResult is what the search tool returns.
type SearchResult struct {
	// Tools are the qualified names (server.tool) of the returned matches, best first.
	Tools []string `json:"tools"`
	// Declarations is TypeScript (.d.ts) for exactly those tools.
	Declarations string `json:"declarations"`
	// Total is the number of matching tools before the top-N cut.
	Total int `json:"total"`
}

// Search returns declarations of the tools subj may call that match query. Tools the subject
// cannot call are filtered out before ranking, so they never influence or appear in the result.
func (s *Service) Search(ctx context.Context, subj policy.Subject, query string) (SearchResult, error) {
	query = truncateRunes(strings.TrimSpace(query), maxQueryChars)
	execID, err := s.audit.StartExecution(ctx, audit.Execution{
		UserID: subj.UserID, ClientID: subj.ClientID, Kind: audit.KindSearch, Code: query,
	})
	if err != nil {
		s.log.Error("codemode: start search audit", "error", err)
		return SearchResult{}, fmt.Errorf("%w: audit unavailable", ErrInternal)
	}
	res, err := s.search(ctx, subj, query)
	status, msg := audit.StatusSucceeded, ""
	if err != nil {
		status, msg = audit.StatusFailed, err.Error()
	}
	if ferr := s.audit.FinishExecution(ctx, execID, status, 0, msg); ferr != nil {
		s.log.Error("codemode: finish search audit", "error", ferr)
	}
	return res, err
}

func (s *Service) search(ctx context.Context, subj policy.Subject, query string) (SearchResult, error) {
	allowed := s.policy.AllowedTools(ctx, subj)
	snap, err := s.catalog.get(ctx)
	if err != nil {
		s.log.Error("codemode: catalog", "error", err)
		return SearchResult{}, fmt.Errorf("%w: tool catalog unavailable", ErrInternal)
	}
	key := searchKey(allowed, query)
	if cached, ok := snap.cachedSearch(key); ok {
		return cached, nil
	}
	defs := make([]catalog.ToolDef, 0, len(allowed))
	for _, ref := range allowed {
		if def, ok := snap.byQualified[catalog.QualifiedName(ref.ServerSlug, ref.Name)]; ok {
			defs = append(defs, def)
		}
	}
	ranked := catalog.Search(defs, query)
	top := ranked[:min(len(ranked), maxSearchResults)]
	res := SearchResult{Tools: make([]string, len(top)), Total: len(ranked)}
	for i, d := range top {
		res.Tools[i] = catalog.QualifiedName(d.Server, d.Name)
	}
	res.Declarations = catalog.TypeScriptWithIdents(append([]catalog.ToolDef(nil), top...), snap.idents)
	snap.storeSearch(key, res)
	return res, nil
}

// searchKey identifies a result by the sorted allowed set (AllowedTools is sorted) and the query.
func searchKey(allowed []policy.ToolRef, query string) string {
	h := sha256.New()
	for _, t := range allowed {
		h.Write([]byte(catalog.QualifiedName(t.ServerSlug, t.Name)))
		h.Write([]byte{'\n'})
	}
	h.Write([]byte{0})
	h.Write([]byte(query))
	return hex.EncodeToString(h.Sum(nil))
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
