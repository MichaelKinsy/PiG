package inproc_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

const upstreamDefaultStaleMessage = "This extension ctx is stale after session replacement or reload. Do not use a captured pi or command ctx after ctx.newSession(), ctx.fork(), ctx.switchSession(), or ctx.reload(). For newSession, fork, and switchSession, move post-replacement work into withSession and use the ctx passed to withSession. For reload, do not use the old ctx after await ctx.reload()."

// newFakeExtension produces a minimally-populated Extension state container.
// All maps are non-nil but empty: matches what a loader produces for a
// factory that did nothing (a degenerate but valid extension).
func newFakeExtension(path string) extension.Extension {
	return extension.Extension{
		Path:             path,
		ResolvedPath:     path,
		Handlers:         map[string][]extension.HandlerFn{},
		Tools:            map[string]extension.RegisteredTool{},
		MessageRenderers: map[string]extension.MessageRenderer{},
		Commands:         map[string]extension.RegisteredCommand{},
		Flags:            map[string]extension.ExtensionFlag{},
		Shortcuts:        map[extension.KeyID]extension.ExtensionShortcut{},
	}
}

// TestNewRunner_Empty: a runner with no extensions reports zero of
// everything and is not stale.
func TestNewRunner_Empty(t *testing.T) {
	r := inproc.NewRunner(nil, "/tmp")

	if got := r.ExtensionPaths(); len(got) != 0 {
		t.Errorf("ExtensionPaths() len = %d, want 0", len(got))
	}
	if got := r.Tools(); len(got) != 0 {
		t.Errorf("Tools() len = %d, want 0 (stub)", len(got))
	}
	if got := r.Commands(); len(got) != 0 {
		t.Errorf("Commands() len = %d, want 0 (stub)", len(got))
	}
	if r.IsStale() {
		t.Errorf("fresh runner reports stale")
	}
	if msg := r.StaleMessage(); msg != "" {
		t.Errorf("fresh runner StaleMessage() = %q, want \"\"", msg)
	}
	// CWD verification goes through Context.CWD() (the public API);
	// Runner has no exported CWD getter (Gap #2 cleanup, F/B pass 2 review).
	extCtx := extension.NewContext("/tmp", nil, r.AssertActiveForTest, extension.ContextActions{})
	if got, err := extCtx.CWD(); err != nil || got != "/tmp" {
		t.Errorf("Context.CWD() = (%q, %v), want (\"/tmp\", nil)", got, err)
	}
}

// TestNewRunner_PreservesExtensionPaths: loaded extensions surface in
// ExtensionPaths in load order.
func TestNewRunner_PreservesExtensionPaths(t *testing.T) {
	exts := []extension.Extension{
		newFakeExtension("/ext/a"),
		newFakeExtension("/ext/b"),
		newFakeExtension("/ext/c"),
	}
	r := inproc.NewRunner(exts, ".")

	got := r.ExtensionPaths()
	want := []string{"/ext/a", "/ext/b", "/ext/c"}
	if len(got) != len(want) {
		t.Fatalf("ExtensionPaths() len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ExtensionPaths()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNewRunner_ExtensionNamesUsesManifestNames(t *testing.T) {
	exts := []extension.Extension{
		{Name: "subagent", Path: "/tmp/pig-build/subagent-abc123", ResolvedPath: "/tmp/pig-build/subagent-abc123"},
		{Name: "context-info", Path: "/tmp/pig-build/context-info-def456", ResolvedPath: "/tmp/pig-build/context-info-def456"},
	}
	r := inproc.NewRunner(exts, ".")

	got := r.ExtensionNames()
	want := []string{"subagent", "context-info"}
	if !slices.Equal(got, want) {
		t.Fatalf("ExtensionNames() = %v, want %v", got, want)
	}
}

// TestInvalidate_SetsStaleWithCustomMessage: the message passed to
// Invalidate is what StaleMessage returns.
func TestInvalidate_SetsStaleWithCustomMessage(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	r.Invalidate("session replaced by user")

	if !r.IsStale() {
		t.Errorf("after Invalidate, IsStale() = false; want true")
	}
	if got, want := r.StaleMessage(), "session replaced by user"; got != want {
		t.Errorf("StaleMessage() = %q, want %q", got, want)
	}
}

// TestInvalidate_DefaultMessageMatchesUpstreamVerbatim: passing "" uses
// the upstream default. The exact string is user-visible and any drift
// would be a fidelity gap.
//
// upstream: runner.ts:461 default parameter value.
func TestInvalidate_DefaultMessageMatchesUpstreamVerbatim(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	r.Invalidate("")

	const upstreamLiteral = upstreamDefaultStaleMessage
	if got := r.StaleMessage(); got != upstreamLiteral {
		t.Errorf("default stale message:\n  got:  %q\n  want: %q (upstream runner.ts:461)", got, upstreamLiteral)
	}
}

// TestInvalidate_FirstWins: the first message wins; subsequent calls
// with different messages are no-ops. Mirrors upstream
// `if (!this.staleMessage)` (runner.ts:462).
func TestInvalidate_FirstWins(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	r.Invalidate("first")
	r.Invalidate("second")
	r.Invalidate("third")

	if got, want := r.StaleMessage(), "first"; got != want {
		t.Errorf("StaleMessage() after multiple Invalidate = %q, want %q (first-wins)", got, want)
	}
}

// TestInvalidate_FirstWinsConcurrent: under racing goroutines, exactly
// one message wins (whichever called Invalidate first). The point is
// not which one: it's that there's no torn state.
func TestInvalidate_FirstWinsConcurrent(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	const N = 50
	messages := make([]string, N)
	for i := range N {
		messages[i] = "msg-" + string(rune('a'+(i%26)))
	}

	var wg sync.WaitGroup
	for _, m := range messages {
		wg.Go(func() { r.Invalidate(m) })
	}
	wg.Wait()

	got := r.StaleMessage()
	found := slices.Contains(messages, got)
	if !found {
		t.Errorf("StaleMessage() = %q, want one of the racing inputs", got)
	}
	if !r.IsStale() {
		t.Errorf("IsStale() = false after concurrent Invalidate")
	}
}

// TestEmit_OnStaleReturnsErrStaleContext: any API method that calls
// assertActive must return an error that matches ErrStaleContext via
// errors.Is. Emit is the canonical example.
//
// **Cross-package gate (Gap #1, F/B pass 2 fix).** This test deliberately
// matches against `extension.ErrStaleContext` (the canonical sentinel
// from the parent package) rather than any in-package alias. Prior to
// the F/B pass 2 cleanup, a duplicate `inproc.ErrStaleContext` shadowed
// the canonical one, causing `errors.Is(err, extension.ErrStaleContext)`
// to silently return false for inproc errors: a compat break invisible
// to in-package tests. There is now exactly one ErrStaleContext
// (defined in coding/extension/errors.go); this test locks that property.
//
// upstream: runner.ts:467-471 (private assertActive throws Error(staleMessage))
func TestEmit_OnStaleReturnsErrStaleContext(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	r.Invalidate("test reason")

	_, err := r.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start"})
	if err == nil {
		t.Fatalf("Emit on stale runner: got nil err; want *StaleError")
	}
	if !errors.Is(err, extension.ErrStaleContext) {
		t.Errorf("Emit on stale: err = %v; want errors.Is(_, ErrStaleContext)", err)
	}

	// The message should ride along on the concrete StaleError.
	var se *inproc.StaleError
	if !errors.As(err, &se) {
		t.Fatalf("Emit on stale: err type = %T; want errors.As to *StaleError", err)
	}
	if se.Message != "test reason" {
		t.Errorf("StaleError.Message = %q, want %q", se.Message, "test reason")
	}
}

// TestEmit_OnFreshNoHandlersReturnsNilNil verifies that an active runner with no
// matching handlers returns no result and no error.
func TestEmit_OnFreshNoHandlersReturnsNilNil(t *testing.T) {
	r := inproc.NewRunner([]extension.Extension{newFakeExtension("/ext/a")}, ".")

	got, err := r.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start"})
	if err != nil {
		t.Errorf("Emit on fresh runner: err = %v; want nil", err)
	}
	if got != nil {
		t.Errorf("Emit on fresh runner: got = %v; want nil (no handlers)", got)
	}
	if got != nil {
		t.Errorf("Emit on fresh runner: got = %v; want nil (no handlers)", got)
	}
}

// TestStubSurfaces_AllReturnEmpty verifies that a missing registration returns
// the documented zero value.
func TestStubSurfaces_AllReturnEmpty(t *testing.T) {
	r := inproc.NewRunner([]extension.Extension{newFakeExtension("/ext/a")}, ".")

	if got := r.MessageRenderer("nonexistent"); got != nil {
		t.Errorf("MessageRenderer(nonexistent) should return nil on miss")
	}
}

// TestStaleError_MatchesCanonicalSentinelOnly verifies that stale runner errors
// use extension.ErrStaleContext across package boundaries and preserve the
// invalidation message.
func TestStaleError_MatchesCanonicalSentinelOnly(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	r.Invalidate("custom reason")

	_, err := r.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start"})
	if err == nil {
		t.Fatal("want non-nil err on stale runner")
	}
	if !errors.Is(err, extension.ErrStaleContext) {
		t.Errorf("errors.Is(err, extension.ErrStaleContext) = false; cross-package sentinel match broken")
	}

	// Verify the concrete error message.
	var se *inproc.StaleError
	if !errors.As(err, &se) {
		t.Fatalf("errors.As to *StaleError failed: %v", err)
	}
	if got := se.Error(); got != "custom reason" {
		t.Errorf("(*StaleError).Error() with non-empty Message = %q, want %q", got, "custom reason")
	}

	// An empty message uses the upstream default.
	empty := &inproc.StaleError{}
	const upstreamDefault = upstreamDefaultStaleMessage
	if got := empty.Error(); got != upstreamDefault {
		t.Errorf("(*StaleError{}).Error() = %q, want upstream default %q", got, upstreamDefault)
	}

	// Sentinel matching does not depend on Message.
	if !errors.Is(empty, extension.ErrStaleContext) {
		t.Error("empty *StaleError does not match extension.ErrStaleContext via errors.Is")
	}
}
