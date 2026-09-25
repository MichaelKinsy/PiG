// Package integration holds end-to-end tests that drive the real
// `pig` binary through a tmux PTY and assert on captured pane output.
//
// These tests are GATED by the `integration` build tag because they:
//
//   - require tmux on PATH
//   - rely on a binary at /tmp/pig-test (built by the harness)
//   - sleep between keystrokes (slow; ~3-5s per test)
//   - depend on terminal width/height behavior that's hard to make
//     deterministic in CI
//
// Run locally with:
//
//	go test -tags integration ./tests/integration/...
//
// CI runs the unit-test tier (everything without this tag) on every
// commit; the integration tier runs nightly or on demand.
//
// What this tier IS for: alt-screen vs inline-flow rendering,
// scrollback preservation across resize, raw-mode SIGINT handling,
// status-line live updates, ANSI-leak from tools. Things only a real
// PTY exposes.
//
// What this tier is NOT for: anything testable as a pure-Go unit
// (input parsing, formatting, dispatch logic). Those belong next to
// the code under test \u2014 keystroke regressions like the Shift+Enter
// bug must be caught by `internal/codingagent/input_split_test.go`,
// not here.
package integration
