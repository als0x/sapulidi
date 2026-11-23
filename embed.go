// Package sapulidi carries the plugin files inside the binary so 'sapulidi agents install' needs no checkout.
package sapulidi

import "embed"

// Plugin holds every host folder of the repository. Each entry is listed on purpose: a new folder is a
// deliberate change.
//
//go:embed .claude-plugin .codex-plugin .cursor-plugin .antigravity-plugin skills hooks bin/sapulidi README.md
var Plugin embed.FS
