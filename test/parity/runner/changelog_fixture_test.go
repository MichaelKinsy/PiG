//go:build parity

package runner

import (
	"path/filepath"
	"testing"
)

// Pi's handleChangelogCommand renders every entry oldest-first (interactive-mode.ts:6505-6524).
// A live release can exceed any fixed viewport; the input, not the pane height, must be pinned.
func TestChangelogScenarioPinsSharedFixture(t *testing.T) {
	root, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	sc, err := LoadScenario(filepath.Join(root, "test/parity/scenarios/slash-commands/05-changelog-ignores-collapse-setting.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if sc.ChangelogFixture == "" {
		t.Fatal("changelog scenario uses growing release documents instead of a shared pinned fixture")
	}
	if !sc.Assert.EscapedOutputEqual || sc.Assert.Runs < 3 {
		t.Fatal("shared changelog must compare the complete escaped block for at least three pairs")
	}
}
