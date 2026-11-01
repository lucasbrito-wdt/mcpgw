package oauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	signingAlg = "ES256"
	// keyReloadInterval bounds DB reloads triggered by unknown kids.
	keyReloadInterval = 30 * time.Second
	p256CoordBytes    = 32
)

// publicJWK is an EC P-256 public key in JWK form (RFC 7517).
type publicJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
}

type jwkSet struct {
	Keys []publicJWK `json:"keys"`
}

type signingKey struct {
	kid  string
	priv *ecdsa.PrivateKey
}

// keyRing holds the active signing key and every public key still valid for verification.
type keyRing struct {
	pool   *pgxpool.Pool
	sealer Sealer
	now    func() time.Time

	mu         sync.RWMutex
	active     *signingKey
	public     map[string]*ecdsa.PublicKey
	lastReload time.Time
}

func newKeyRing(pool *pgxpool.Pool, sealer Sealer, now func() time.Time) *keyRing {
	return &keyRing{pool: pool, sealer: sealer, now: now, public: map[string]*ecdsa.PublicKey{}}
}

func signingKeyAAD(kid string) []byte { return []byte("mcpgw/signing-key/" + kid) }

// CheckSigningKey reports whether sealer can open the stored active signing key. A database
// without one passes (fresh install); nothing is created.
func CheckSigningKey(ctx context.Context, pool *pgxpool.Pool, sealer Sealer) error {
	var kid string
	var sealed []byte
	err := pool.QueryRow(ctx, `SELECT kid, private_key_ciphertext FROM signing_keys
		WHERE alg = $1 AND retired_at IS NULL ORDER BY created_at DESC LIMIT 1`, signingAlg).Scan(&kid, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = newKeyRing(pool, sealer, time.Now).openPrivateKey(kid, sealed)
	return err
}

// ensureActive loads the newest non-retired key, creating one when none exists.
func (k *keyRing) ensureActive(ctx context.Context) error {
	var kid string
	var sealed []byte
	err := k.pool.QueryRow(ctx, `SELECT kid, private_key_ciphertext FROM signing_keys
		WHERE alg = $1 AND retired_at IS NULL ORDER BY created_at DESC LIMIT 1`, signingAlg).Scan(&kid, &sealed)
	if errors.Is(err, pgx.ErrNoRows) {
		kid, sealed, err = k.createKey(ctx)
	}
	if err != nil {
		return err
	}
	priv, err := k.openPrivateKey(kid, sealed)
	if err != nil {
		return err
	}
	k.mu.Lock()
	k.active = &signingKey{kid: kid, priv: priv}
	k.mu.Unlock()
	return k.reload(ctx)
}

func (k *keyRing) createKey(ctx context.Context) (string, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", nil, err
	}
	jwk, err := toPublicJWK(&priv.PublicKey)
	if err != nil {
		return "", nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", nil, err
	}
	sealed, err := k.sealer.Seal(der, signingKeyAAD(jwk.Kid))
	if err != nil {
		return "", nil, fmt.Errorf("seal signing key: %w", err)
	}
	jwkJSON, err := json.Marshal(jwk)
	if err != nil {
		return "", nil, err
	}
	_, err = k.pool.Exec(ctx, `INSERT INTO signing_keys (kid, alg, private_key_ciphertext, public_jwk)
		VALUES ($1, $2, $3, $4)`, jwk.Kid, signingAlg, sealed, jwkJSON)
	return jwk.Kid, sealed, err
}

func (k *keyRing) openPrivateKey(kid string, sealed []byte) (*ecdsa.PrivateKey, error) {
	der, err := k.sealer.Open(sealed, signingKeyAAD(kid))
	if err != nil {
		return nil, fmt.Errorf("open signing key %s: %w", kid, err)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	priv, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || priv.Curve != elliptic.P256() {
		return nil, fmt.Errorf("signing key %s is not P-256", kid)
	}
	return priv, nil
}

// verificationKeys lists keys that may still have unexpired tokens in circulation.
func (k *keyRing) verificationKeys(ctx context.Context) ([]publicJWK, error) {
	cutoff := k.now().Add(-accessTokenTTL - validationLeeway)
	rows, err := k.pool.Query(ctx, `SELECT public_jwk FROM signing_keys
		WHERE alg = $1 AND (retired_at IS NULL OR retired_at > $2) ORDER BY created_at DESC`, signingAlg, cutoff)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (publicJWK, error) {
		var jwk publicJWK
		err := row.Scan(&jwk)
		return jwk, err
	})
}

func (k *keyRing) reload(ctx context.Context) error {
	jwks, err := k.verificationKeys(ctx)
	if err != nil {
		return err
	}
	public := make(map[string]*ecdsa.PublicKey, len(jwks))
	for _, jwk := range jwks {
		pub, err := jwk.publicKey()
		if err != nil {
			return fmt.Errorf("signing key %s: %w", jwk.Kid, err)
		}
		public[jwk.Kid] = pub
	}
	k.mu.Lock()
	k.public = public
	k.lastReload = k.now()
	k.mu.Unlock()
	return nil
}

// lookup returns the public key for kid, reloading from the DB at most once per interval.
func (k *keyRing) lookup(ctx context.Context, kid string) (*ecdsa.PublicKey, error) {
	k.mu.RLock()
	pub, ok := k.public[kid]
	stale := k.now().Sub(k.lastReload) > keyReloadInterval
	k.mu.RUnlock()
	if ok {
		return pub, nil
	}
	if !stale {
		return nil, errors.New("unknown kid")
	}
	if err := k.reload(ctx); err != nil {
		return nil, err
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	if pub, ok = k.public[kid]; !ok {
		return nil, errors.New("unknown kid")
	}
	return pub, nil
}

func (k *keyRing) signer() *signingKey {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.active
}

func (k *keyRing) publicJWKS(ctx context.Context) (jwkSet, error) {
	keys, err := k.verificationKeys(ctx)
	if keys == nil {
		keys = []publicJWK{}
	}
	return jwkSet{Keys: keys}, err
}

func toPublicJWK(pub *ecdsa.PublicKey) (publicJWK, error) {
	raw, err := pub.Bytes()
	if err != nil {
		return publicJWK{}, err
	}
	b64 := base64.RawURLEncoding.EncodeToString
	jwk := publicJWK{
		Kty: "EC", Crv: "P-256", Alg: signingAlg, Use: "sig",
		X: b64(raw[1 : 1+p256CoordBytes]),
		Y: b64(raw[1+p256CoordBytes:]),
	}
	// RFC 7638 thumbprint: required members in lexicographic order, no whitespace.
	thumb := sha256.Sum256([]byte(`{"crv":"P-256","kty":"EC","x":"` + jwk.X + `","y":"` + jwk.Y + `"}`))
	jwk.Kid = b64(thumb[:])
	return jwk, nil
}

func (j publicJWK) publicKey() (*ecdsa.PublicKey, error) {
	if j.Kty != "EC" || j.Crv != "P-256" || j.Alg != signingAlg {
		return nil, errors.New("unsupported jwk")
	}
	x, errX := base64.RawURLEncoding.DecodeString(j.X)
	y, errY := base64.RawURLEncoding.DecodeString(j.Y)
	if errX != nil || errY != nil || len(x) != p256CoordBytes || len(y) != p256CoordBytes {
		return nil, errors.New("malformed jwk coordinates")
	}
	raw := append(append([]byte{4}, x...), y...)
	return ecdsa.ParseUncompressedPublicKey(elliptic.P256(), raw)
}
