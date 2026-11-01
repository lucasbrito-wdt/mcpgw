package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/config"
	"github.com/lucasbrito-wdt/mcpgw/internal/i18n"
	"github.com/lucasbrito-wdt/mcpgw/internal/oauth"
	"github.com/lucasbrito-wdt/mcpgw/internal/store/storetest"
	"github.com/lucasbrito-wdt/mcpgw/internal/vault"
)

func randomMasterKey(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestMigrateDetectsMasterKeyMismatch(t *testing.T) {
	pool := storetest.New(t)
	t.Setenv(config.EnvDatabaseURL, pool.Config().ConnString())
	migrate := func(key string) error {
		t.Setenv(config.EnvMasterKey, key)
		return runMigrate(context.Background(), nil, &bytes.Buffer{}, i18n.EN)
	}

	original := randomMasterKey(t)
	if err := migrate(original); err != nil { // no signing key yet: fresh database passes
		t.Fatalf("fresh database: %v", err)
	}
	raw, _ := base64.StdEncoding.DecodeString(original)
	v, err := vault.New(raw, vaultKeyID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oauth.New(context.Background(), oauth.Options{PublicURL: "http://localhost:8787", Pool: pool, Sealer: v}); err != nil {
		t.Fatal(err)
	}

	if err := migrate(original); err != nil {
		t.Fatalf("same key: %v", err)
	}
	err = migrate(randomMasterKey(t))
	if err == nil || !errors.Is(err, vault.ErrDecrypt) || !strings.Contains(i18n.Message(err, i18n.EN), "does not match the key that encrypted this database") {
		t.Fatalf("different key: %v", err)
	}
}
