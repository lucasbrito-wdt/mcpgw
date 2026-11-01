// Package migrations embeds the SQL migration files.
package migrations

import "embed"

// FS holds every NNNN_name.sql file at its root.
//
//go:embed *.sql
var FS embed.FS
