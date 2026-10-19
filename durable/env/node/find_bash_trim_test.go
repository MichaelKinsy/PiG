//go:build !windows

package node

import (
	"os"
	"path/filepath"
	"testing"
)

// node.ts:198 takes `result.stdout.trim().split(/\r?\n/)[0]` of `which bash`: trim removes a leading BOM, so the path behind it is found.
// The answer is read from the text rather than from a `which` the test would start: node.ts:195-196 gives that command 5 s to start and
// answer, a limit a machine too loaded to start a process in time would turn into a missing bash.
func TestFindBashOnPathTrimsTheAnswerLikeJavaScript(t *testing.T) {
	bash := filepath.Join(t.TempDir(), "bash")
	mustDo(t, os.WriteFile(bash, nil, 0o700))
	if got := bashFromWhichOutput("\ufeff" + bash + "\r\nother\n"); got != bash {
		t.Fatalf("bashFromWhichOutput = %q, want %q", got, bash)
	}
}
