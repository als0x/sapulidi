// Package packs embeds the built-in rule packs (packs/*.toml). The loader in internal/packs
// reads only the *.toml files, so a pack added later needs no edit here.
package packs

import "embed"

//go:embed *
var FS embed.FS
