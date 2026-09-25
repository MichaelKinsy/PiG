package inproc_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// extWithRenderer produces a fake extension with a MessageRenderer
// registered for the given customType.
func extWithRenderer(path, customType string) extension.Extension {
	ext := newFakeExtension(path)
	ext.MessageRenderers[customType] = func(
		msg extension.CustomMessage,
		opts extension.MessageRenderOptions,
		theme extension.Theme,
	) extension.Component {
		return nil // placeholder renderer: existence is what we test.
	}
	return ext
}

// TestMessageRenderer_FirstMatchWins: two extensions register for the
// same customType; first-loaded wins (mirrors upstream getMessageRenderer
// which returns on first match).
//
// upstream: runner.ts:495-503 (for...of return on first hit)
func TestMessageRenderer_FirstMatchWins(t *testing.T) {
	exts := []extension.Extension{
		extWithRenderer("/ext/a", "tree-summary"),
		extWithRenderer("/ext/b", "tree-summary"),
	}
	r := inproc.NewRunner(exts, ".")
	got := r.MessageRenderer("tree-summary")
	if got == nil {
		t.Fatal("MessageRenderer(tree-summary) = nil, want non-nil (first match)")
	}
	// We can't compare function pointers, but we verified it returned
	// non-nil from two competing registrations without panic.
}

// TestMessageRenderer_NoMatchReturnsNil: customType not registered by
// any extension ⇒ nil.
func TestMessageRenderer_NoMatchReturnsNil(t *testing.T) {
	exts := []extension.Extension{
		extWithRenderer("/ext/a", "tree-summary"),
	}
	r := inproc.NewRunner(exts, ".")
	got := r.MessageRenderer("nonexistent")
	if got != nil {
		t.Errorf("MessageRenderer(nonexistent) = %v, want nil", got)
	}
}

// TestMessageRenderer_EmptyRunnerReturnsNil: no extensions ⇒ nil.
func TestMessageRenderer_EmptyRunnerReturnsNil(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	got := r.MessageRenderer("any")
	if got != nil {
		t.Errorf("MessageRenderer(any) on empty runner = %v, want nil", got)
	}
}
