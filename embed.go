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
	_ "embed"
	"unsafe"

	"github.com/MichaelKinsy/PiG/internal/zstdblob"
)

// The binary carries the changelog as one zstd frame; CHANGELOG.md is the same text uncompressed, the file
// authors edit. TestBundledChangelogFrameMatchesSource fails when the two differ.
//
//go:generate go run ./automation/gen/zstdfile CHANGELOG.md CHANGELOG.md.zst
//go:embed CHANGELOG.md.zst
var changelogFrame []byte

// decodeChangelog decodes the embedded changelog. It is not cached: the caller (the startup What's New check
// and /changelog) parses it and drops the text, so the ~1 MB string never stays on the live heap of an idle
// session (TestTmuxIdleMemoryGate). Each call costs about two milliseconds and happens at most once per action.
func decodeChangelog() string {
	text, err := zstdblob.Decode(changelogFrame)
	if err != nil {
		panic("pig: embedded CHANGELOG.md.zst: " + err.Error())
	}
	// The decoded buffer is this call's own and is never written again, so the string takes it without a second copy of the whole changelog.
	return unsafe.String(unsafe.SliceData(text), len(text)) //nolint:gosec // G103: text is this call's own buffer and is never written again, so the string aliases it without a second 1 MB copy; nothing else holds the slice
}

// Changelog returns the bundled CHANGELOG.md, decoded on each call and not cached. Read by internal/codingagent's
// `/changelog` slash handler and the startup What's New.
func Changelog() string { return decodeChangelog() }
