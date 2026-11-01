// Package migrations embeds the SQL files applied at start-up, in file-name order.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
