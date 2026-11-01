package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"
)

const cimdCacheMaxEntries = 1000

type cachedClient struct {
	client  *Client
	expires time.Time
}

// clientResolver resolves client_ids: CIMD URLs through an in-memory cache backed by the
// guarded fetcher (and persisted in oauth_clients for FKs), other ids from oauth_clients.
type clientResolver struct {
	pool         *pgxpool.Pool
	fetcher      *cimdFetcher
	now          func() time.Time
	fetchLimiter *keyedLimiter

	mu    sync.Mutex
	cache map[string]cachedClient
}

func newClientResolver(pool *pgxpool.Pool, fetcher *cimdFetcher, now func() time.Time) *clientResolver {
	return &clientResolver{
		pool:         pool,
		fetcher:      fetcher,
		now:          now,
		fetchLimiter: newKeyedLimiter(rate.Every(6*time.Second), 10),
		cache:        map[string]cachedClient{},
	}
}

// resolve returns the client for clientID. requester keys the rate limit on remote fetches.
func (r *clientResolver) resolve(ctx context.Context, clientID, requester string) (*Client, error) {
	if !isCIMDClientID(clientID) {
		return r.loadStored(ctx, clientID, kindDCR)
	}
	if c, ok := r.cached(clientID); ok {
		return c, nil
	}
	if !r.fetchLimiter.allow(requester, r.now()) {
		return nil, errors.New("too many client metadata fetches")
	}
	c, ttl, err := r.fetchCIMD(ctx, clientID)
	if err != nil {
		return nil, err
	}
	if err := r.persistCIMD(ctx, c); err != nil {
		return nil, err
	}
	r.store(c, ttl)
	return c, nil
}

func (r *clientResolver) fetchCIMD(ctx context.Context, clientID string) (*Client, time.Duration, error) {
	body, ttl, err := r.fetcher.fetch(ctx, clientID)
	if err != nil {
		return nil, 0, err
	}
	var meta ClientMetadata
	if err := json.Unmarshal(body, &meta); err != nil {
		return nil, 0, fmt.Errorf("client metadata is not valid JSON: %w", err)
	}
	if meta.ClientID != clientID {
		return nil, 0, errors.New("client metadata client_id does not match its URL")
	}
	if meta.ClientName == "" {
		return nil, 0, errors.New("client metadata must include client_name")
	}
	if err := meta.normalize(); err != nil {
		return nil, 0, fmt.Errorf("client metadata: %w", err)
	}
	return &Client{ClientID: clientID, Kind: kindCIMD, Metadata: meta}, ttl, nil
}

func (r *clientResolver) persistCIMD(ctx context.Context, c *Client) error {
	meta, err := json.Marshal(c.Metadata)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO oauth_clients
		(client_id, registration_kind, metadata, redirect_uris, fetched_at)
		VALUES ($1, 'cimd', $2, $3, $4)
		ON CONFLICT (client_id) DO UPDATE SET metadata = EXCLUDED.metadata,
			redirect_uris = EXCLUDED.redirect_uris, fetched_at = EXCLUDED.fetched_at
		WHERE oauth_clients.registration_kind = 'cimd'`,
		c.ClientID, meta, c.Metadata.RedirectURIs, r.now())
	return err
}

// loadStored reads a client from oauth_clients. kind "" accepts any kind.
func (r *clientResolver) loadStored(ctx context.Context, clientID, kind string) (*Client, error) {
	c := &Client{ClientID: clientID}
	var raw []byte
	err := r.pool.QueryRow(ctx, `SELECT registration_kind, metadata FROM oauth_clients
		WHERE client_id = $1`, clientID).Scan(&c.Kind, &raw)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && kind != "" && c.Kind != kind) {
		return nil, errUnknownClient
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &c.Metadata); err != nil {
		return nil, fmt.Errorf("stored client metadata: %w", err)
	}
	return c, nil
}

func (r *clientResolver) cached(clientID string) (*Client, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.cache[clientID]
	if !ok || !r.now().Before(e.expires) {
		return nil, false
	}
	return e.client, true
}

func (r *clientResolver) store(c *Client, ttl time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if len(r.cache) >= cimdCacheMaxEntries {
		for id, e := range r.cache {
			if !now.Before(e.expires) {
				delete(r.cache, id)
			}
		}
	}
	for id := range r.cache {
		if len(r.cache) < cimdCacheMaxEntries {
			break
		}
		delete(r.cache, id)
	}
	r.cache[c.ClientID] = cachedClient{client: c, expires: now.Add(ttl)}
}
