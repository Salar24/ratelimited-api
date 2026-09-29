// Package migrations embeds the database schema.
package migrations

import _ "embed"

// Schema is idempotent and applied on startup.
//
//go:embed schema.sql
var Schema string
