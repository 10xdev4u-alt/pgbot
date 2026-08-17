// Package docs embeds the findings catalogue into the binary so
// `pgbot explain-finding <id>` works offline and in air-gapped environments —
// which is often exactly where a database health tool runs.
package docs

import "embed"

// Findings holds docs/findings/<id>.md (the _template.md is excluded by the glob,
// which skips underscore-prefixed names).
//
//go:embed findings/*.md
var Findings embed.FS
