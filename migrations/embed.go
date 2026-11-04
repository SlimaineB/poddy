// Package migrations embeds the PostgreSQL schema migrations so the portal or
// bastion can apply them on startup when a database URL is configured.
package migrations

import "embed"

// FS contains the embedded SQL migration files.
//
//go:embed *.sql
var FS embed.FS
