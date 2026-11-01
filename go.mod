module github.com/lucasbrito-wdt/mcpgw

go 1.26.0

require (
	github.com/fastschema/qjs v0.0.6
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/jackc/pgx/v5 v5.11.0
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/tetratelabs/wazero v1.12.0
	golang.org/x/crypto v0.58.0
	golang.org/x/sync v0.24.0
	golang.org/x/sys v0.49.0
	golang.org/x/time v0.16.0
)

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

// Local copy of qjs v0.0.6 with one patch in runtime.go (search "mcpgw patch"):
// New no longer hashes the 1 MB wasm under a global mutex on every call.
replace github.com/fastschema/qjs => ./third_party/qjs
