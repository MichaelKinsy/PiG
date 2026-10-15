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

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
)

// The binary carries the changelog gzip-compressed; CHANGELOG.md is the same text uncompressed, the file
// authors edit. TestBundledChangelogGzipMatchesSource fails when the two differ.
//
//go:generate sh -c "gzip -9nc CHANGELOG.md > CHANGELOG.md.gz"
//go:embed CHANGELOG.md.gz
var changelogGzip []byte

// inflateChangelog decodes the embedded changelog. It is not cached: the caller (the startup What's New check
// and /changelog) parses it and drops the text, so the ~616 KB string never stays on the live heap of an idle
// session (TestTmuxIdleMemoryGate). Each call costs a few milliseconds and happens at most once per action.
func inflateChangelog() string {
	reader, err := gzip.NewReader(bytes.NewReader(changelogGzip))
	if err != nil {
		panic("pig: embedded CHANGELOG.md.gz: " + err.Error())
	}
	text, err := io.ReadAll(reader)
	if err != nil {
		panic("pig: embedded CHANGELOG.md.gz: " + err.Error())
	}
	return string(text)
}

// Changelog returns the bundled CHANGELOG.md, inflated on each call and not cached. Read by internal/codingagent's
// `/changelog` slash handler and the startup What's New.
func Changelog() string { return inflateChangelog() }
