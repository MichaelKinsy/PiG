// Package pig exposes build-time-embedded resources for the pig binary.
//
// This is the only file at module root; runtime code lives in cmd/, internal/,
// coding/. Its sole purpose is to host `//go:embed` directives that need
// access to files at the repo root (Go embed cannot traverse to parent
// directories from a sub-package).
//
// Currently embeds:
// - CHANGELOG.md, surfaced via the `/changelog` slash command.
package pig

import _ "embed"

// Changelog is the bundled CHANGELOG.md, embedded at build time. Read by
// internal/codingagent's `/changelog` slash handler. Empty when the file
// is missing at build time (which would fail the embed and the build).
//
//go:embed CHANGELOG.md
var Changelog string
