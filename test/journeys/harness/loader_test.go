package harness

import "testing"

// Idle must not return while a loader runs. Pi 1.1.0 embeds the working status in the editor's top border (custom-editor.ts embedWorkingStatus); a
// row-start pattern missed it, so the markdown step of startup-markdown-quit snapped Pi before its answer (JOURNEY_SEED=20).
func TestHasLoaderSeesEveryLoaderForm(t *testing.T) {
	for _, tc := range []struct {
		name   string
		screen string
		want   bool
	}{
		{"own row", " MD render everything\n ⠋ Working...\n", true},
		{"embedded in the editor border", " MD render everything\n── ⠋ Working ─────────────────\n\n──────────\n", true},
		{"settled", " Echo: MD render everything\n──────────\n\n──────────\n", false},
	} {
		if got := hasLoader(tc.screen); got != tc.want {
			t.Errorf("%s: hasLoader = %v, want %v", tc.name, got, tc.want)
		}
	}
}
