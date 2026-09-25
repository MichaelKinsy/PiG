package inproc_test

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// extWithCommand produces a fake extension at the given path with one
// registered command of the given name.
func extWithCommand(path, name, description string) extension.Extension {
	ext := newFakeExtension(path)
	ext.Commands[name] = extension.RegisteredCommand{
		Name:        name,
		Description: description,
		Handler:     func(ctx context.Context, args string) error { return nil },
	}
	return ext
}

// extWithFlag produces a fake extension at the given path with one
// registered flag declaration.
func extWithFlag(path, name, description string) extension.Extension {
	ext := newFakeExtension(path)
	ext.Flags[name] = extension.ExtensionFlag{
		Name:        name,
		Description: description,
	}
	return ext
}

// extWithShortcut produces a fake extension at the given path with one
// registered shortcut bound to the given key.
func extWithShortcut(path, key, description string) extension.Extension {
	ext := newFakeExtension(path)
	ext.Shortcuts[extension.KeyID(key)] = extension.ExtensionShortcut{
		Shortcut:      extension.KeyID(key),
		Description:   description,
		Handler:       func(ctx context.Context) error { return nil },
		ExtensionPath: path,
	}
	return ext
}

// ─── Commands() ───────────────────────────────────────────────────────────

// TestCommands_Empty: no extensions ⇒ empty slice (non-nil).
func TestCommands_Empty(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	got := r.Commands()
	if got == nil {
		t.Errorf("Commands() = nil, want non-nil empty slice")
	}
	if len(got) != 0 {
		t.Errorf("Commands() len = %d, want 0", len(got))
	}
}

// TestCommands_UniqueNamesKeepBareInvocation: distinct command names
// across extensions ⇒ each gets its bare name as invocationName.
//
// upstream: runner.ts:521 (`counts.get(command.name) > 1` triggers suffix)
func TestCommands_UniqueNamesKeepBareInvocation(t *testing.T) {
	exts := []extension.Extension{
		extWithCommand("/ext/a", "deploy", "deploy stuff"),
		extWithCommand("/ext/b", "rollback", "undo stuff"),
	}
	r := inproc.NewRunner(exts, ".")

	got := r.Commands()
	if len(got) != 2 {
		t.Fatalf("Commands() len = %d, want 2", len(got))
	}
	for _, cmd := range got {
		if cmd.InvocationName != cmd.Name {
			t.Errorf("InvocationName = %q, want bare name %q (no collision)", cmd.InvocationName, cmd.Name)
		}
	}
}

// TestCommands_DuplicateNamesGetSuffixDisambiguation: two extensions
// register `cmd` ⇒ invocation names `cmd:1` and `cmd:2`.
//
// upstream: runner.ts:519-525 (counts > 1 → name:occurrence)
func TestCommands_DuplicateNamesGetSuffixDisambiguation(t *testing.T) {
	exts := []extension.Extension{
		extWithCommand("/ext/first", "deploy", "first"),
		extWithCommand("/ext/second", "deploy", "second"),
		extWithCommand("/ext/third", "deploy", "third"),
	}
	r := inproc.NewRunner(exts, ".")

	got := r.Commands()
	if len(got) != 3 {
		t.Fatalf("Commands() len = %d, want 3 (no dedup)", len(got))
	}
	// Each invocation name must be unique and follow `name:occurrence` pattern.
	seen := map[string]struct{}{}
	for _, cmd := range got {
		if cmd.Name != "deploy" {
			t.Errorf("cmd.Name = %q, want %q", cmd.Name, "deploy")
		}
		if _, dup := seen[cmd.InvocationName]; dup {
			t.Errorf("duplicate invocationName: %q", cmd.InvocationName)
		}
		seen[cmd.InvocationName] = struct{}{}
	}
	// Expected invocation names from upstream algorithm
	for _, want := range []string{"deploy:1", "deploy:2", "deploy:3"} {
		if _, ok := seen[want]; !ok {
			t.Errorf("expected invocationName %q not present; got: %v", want, seen)
		}
	}
}

// TestCommands_ClearsDiagnosticsBuffer: each Commands() call resets the
// diagnostics buffer (mirrors upstream `this.commandDiagnostics = []`).
//
// upstream: runner.ts:541-543 (getRegisteredCommands)
func TestCommands_ClearsDiagnosticsBuffer(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	// Empty buffer initially.
	if d := r.CommandDiagnostics(); len(d) != 0 {
		t.Errorf("initial CommandDiagnostics len = %d, want 0", len(d))
	}
	// Call Commands(): should still produce no diagnostics on empty input.
	r.Commands()
	if d := r.CommandDiagnostics(); len(d) != 0 {
		t.Errorf("after empty Commands() CommandDiagnostics len = %d, want 0", len(d))
	}
}

// ─── Command(name) ────────────────────────────────────────────────────────

// TestCommand_FoundByInvocationName: lookup uses invocationName not Name.
//
// upstream: runner.ts:550-552 (find by invocationName)
func TestCommand_FoundByInvocationName(t *testing.T) {
	exts := []extension.Extension{
		extWithCommand("/ext/a", "deploy", "first"),
		extWithCommand("/ext/b", "deploy", "second"),
	}
	r := inproc.NewRunner(exts, ".")

	if _, ok := r.Command("deploy"); ok {
		t.Errorf("Command(\"deploy\") found despite collision; should look for invocationName")
	}
	if _, ok := r.Command("deploy:1"); !ok {
		t.Errorf("Command(\"deploy:1\") not found")
	}
	if _, ok := r.Command("deploy:2"); !ok {
		t.Errorf("Command(\"deploy:2\") not found")
	}
}

// TestCommand_NotFound: missing name ⇒ zero value + false.
func TestCommand_NotFound(t *testing.T) {
	r := inproc.NewRunner([]extension.Extension{
		extWithCommand("/ext/a", "deploy", ""),
	}, ".")

	cmd, ok := r.Command("nonexistent")
	if ok {
		t.Errorf("Command(\"nonexistent\") found, want not found")
	}
	if cmd.Name != "" {
		t.Errorf("not-found cmd should be zero; got Name=%q", cmd.Name)
	}
}

// ─── Flags() ──────────────────────────────────────────────────────────────

// TestFlags_FirstWinsOnNameCollision: matches Tools first-wins.
//
// upstream: runner.ts:392-402 (getFlags)
func TestFlags_FirstWinsOnNameCollision(t *testing.T) {
	exts := []extension.Extension{
		extWithFlag("/ext/first", "verbose", "from first"),
		extWithFlag("/ext/second", "verbose", "from second (should be hidden)"),
	}
	r := inproc.NewRunner(exts, ".")

	got := r.Flags()
	if len(got) != 1 {
		t.Fatalf("Flags() len = %d, want 1 (dedup)", len(got))
	}
	if got["verbose"].Description != "from first" {
		t.Errorf("Flags()[verbose].Description = %q, want %q (first-wins)",
			got["verbose"].Description, "from first")
	}
}

// TestFlags_AggregatesAcrossExtensions: distinct flag names ⇒ all surface.
func TestFlags_AggregatesAcrossExtensions(t *testing.T) {
	exts := []extension.Extension{
		extWithFlag("/ext/a", "verbose", "v"),
		extWithFlag("/ext/b", "debug", "d"),
		extWithFlag("/ext/c", "trace", "t"),
	}
	r := inproc.NewRunner(exts, ".")

	got := r.Flags()
	for _, name := range []string{"verbose", "debug", "trace"} {
		if _, ok := got[name]; !ok {
			t.Errorf("Flags() missing %q", name)
		}
	}
	if len(got) != 3 {
		t.Errorf("Flags() len = %d, want 3", len(got))
	}
}

// ─── Shortcuts() ──────────────────────────────────────────────────────────

// TestShortcuts_NormalizesKeysToLowercase: upstream keys are normalized
// via `toLowerCase()` (runner.ts:425). Two extensions registering
// "Ctrl+R" and "ctrl+r" must collide.
func TestShortcuts_NormalizesKeysToLowercase(t *testing.T) {
	exts := []extension.Extension{
		extWithShortcut("/ext/a", "Ctrl+R", "first"),
		extWithShortcut("/ext/b", "CTRL+r", "second"),
	}
	r := inproc.NewRunner(exts, ".")

	got := r.Shortcuts(nil)
	if len(got) != 1 {
		t.Errorf("Shortcuts() len = %d, want 1 (case-normalized dedup)", len(got))
	}
	if _, ok := got[extension.KeyID("ctrl+r")]; !ok {
		t.Errorf("normalized key 'ctrl+r' not in result: %v", got)
	}
	if d := r.ShortcutDiagnostics(); len(d) != 1 {
		t.Errorf("ShortcutDiagnostics len = %d, want 1 (collision)", len(d))
	}
}

// TestShortcuts_LastWinsOnConflict: when two extensions register the
// same key, the LATER one is kept (opposite direction from
// Tools/Flags first-wins).
//
// upstream: runner.ts:444-451 (assignment overwrites after diagnostic)
func TestShortcuts_LastWinsOnConflict(t *testing.T) {
	exts := []extension.Extension{
		extWithShortcut("/ext/first", "ctrl+r", "from first (should be hidden)"),
		extWithShortcut("/ext/second", "ctrl+r", "from second (should win)"),
	}
	r := inproc.NewRunner(exts, ".")

	got := r.Shortcuts(nil)
	if len(got) != 1 {
		t.Fatalf("Shortcuts() len = %d, want 1", len(got))
	}
	sc := got[extension.KeyID("ctrl+r")]
	if sc.Description != "from second (should win)" {
		t.Errorf("description = %q, want last-wins (\"from second...\")", sc.Description)
	}
	if sc.ExtensionPath != "/ext/second" {
		t.Errorf("ExtensionPath = %q, want /ext/second", sc.ExtensionPath)
	}
}

// TestShortcuts_DiagnosticMessageMatchesUpstream: the warning string is
// user-visible (logged to console + surfaced in TUI). Drift would be a
// fidelity gap.
//
// upstream: runner.ts:445-449 (template literal verbatim)
func TestShortcuts_DiagnosticMessageMatchesUpstream(t *testing.T) {
	exts := []extension.Extension{
		extWithShortcut("/ext/a", "ctrl+r", "first"),
		extWithShortcut("/ext/b", "ctrl+r", "second"),
	}
	r := inproc.NewRunner(exts, ".")
	r.Shortcuts(nil) // populate diagnostics

	d := r.ShortcutDiagnostics()
	if len(d) != 1 {
		t.Fatalf("ShortcutDiagnostics len = %d, want 1", len(d))
	}
	// Upstream message format:
	// `Extension shortcut conflict: '<key>' registered by both <extA> and <extB>. Using <extB>.`
	// pig simplifies to "registered by multiple extensions" because we
	// don't track who registered first across the loop in a way that's
	// useful: the LATER registrant wins, and upstream's "Using <extB>"
	// suffix conveys that. Locked here so future drift is visible.
	want := `Extension shortcut conflict: 'ctrl+r' registered by multiple extensions. Using /ext/b.`
	if d[0].Message != want {
		t.Errorf("diagnostic message:\n  got:  %q\n  want: %q", d[0].Message, want)
	}
	if d[0].Type != extension.DiagnosticWarning {
		t.Errorf("diagnostic Type = %q, want %q", d[0].Type, extension.DiagnosticWarning)
	}
	if d[0].Path != "/ext/b" {
		t.Errorf("diagnostic Path = %q, want /ext/b", d[0].Path)
	}
}

func TestShortcutsReservedBuiltinIsRejected(t *testing.T) {
	r := inproc.NewRunner([]extension.Extension{
		extWithShortcut("/ext/a", "ctrl+c", "must not replace clear"),
	}, ".")
	bindings := map[string][]string{"app.clear": {"ctrl+c"}}

	if got := r.Shortcuts(bindings); len(got) != 0 {
		t.Fatalf("reserved shortcut was registered: %v", got)
	}
	diagnostics := r.ShortcutDiagnostics()
	if len(diagnostics) != 1 || diagnostics[0].Message != "Extension shortcut 'ctrl+c' from /ext/a conflicts with built-in shortcut. Skipping." {
		t.Fatalf("reserved diagnostic = %#v", diagnostics)
	}
}

func TestShortcutsOverridableBuiltinWarnsAndUsesExtension(t *testing.T) {
	r := inproc.NewRunner([]extension.Extension{
		extWithShortcut("/ext/a", "shift+l", "override tree label"),
	}, ".")
	bindings := map[string][]string{"app.tree.editLabel": {"shift+l"}}

	if got := r.Shortcuts(bindings); got["shift+l"].ExtensionPath != "/ext/a" {
		t.Fatalf("overridable shortcut = %#v", got)
	}
	diagnostics := r.ShortcutDiagnostics()
	if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].Message, "is built-in shortcut for app.tree.editLabel") {
		t.Fatalf("overridable diagnostic = %#v", diagnostics)
	}
}

func TestSetKeybindingsReservedActionWinsSharedKey(t *testing.T) {
	r := inproc.NewRunner([]extension.Extension{
		extWithShortcut("/ext/a", "ctrl+d", "must not replace exit"),
	}, ".")
	bindings := map[string][]string{
		"app.session.delete": {"ctrl+d"},
		"app.exit":           {"ctrl+d"},
	}
	if got := r.Shortcuts(bindings); len(got) != 0 {
		t.Fatalf("reserved binding lost to non-reserved action: %v", got)
	}
}

// TestShortcuts_ClearsDiagnosticsBuffer: each Shortcuts() call resets the
// diagnostics buffer (mirrors upstream `this.shortcutDiagnostics = []`
// at runner.ts:413).
func TestShortcuts_ClearsDiagnosticsBuffer(t *testing.T) {
	exts := []extension.Extension{
		extWithShortcut("/ext/a", "ctrl+r", "first"),
		extWithShortcut("/ext/b", "ctrl+r", "second"),
	}
	r := inproc.NewRunner(exts, ".")

	r.Shortcuts(nil)
	if d := r.ShortcutDiagnostics(); len(d) != 1 {
		t.Fatalf("after first Shortcuts(): diagnostics len = %d, want 1", len(d))
	}
	// Second call should clear and re-populate identically.
	r.Shortcuts(nil)
	if d := r.ShortcutDiagnostics(); len(d) != 1 {
		t.Errorf("after second Shortcuts(): diagnostics len = %d, want 1 (reset+repop)", len(d))
	}
}

// TestShortcuts_NoConflictNoDiagnostic: clean shortcuts emit no warnings.
func TestShortcuts_NoConflictNoDiagnostic(t *testing.T) {
	exts := []extension.Extension{
		extWithShortcut("/ext/a", "ctrl+r", "r"),
		extWithShortcut("/ext/b", "ctrl+t", "t"),
	}
	r := inproc.NewRunner(exts, ".")

	got := r.Shortcuts(nil)
	if len(got) != 2 {
		t.Errorf("Shortcuts() len = %d, want 2", len(got))
	}
	if d := r.ShortcutDiagnostics(); len(d) != 0 {
		t.Errorf("ShortcutDiagnostics len = %d, want 0 (no conflicts)", len(d))
	}
}
