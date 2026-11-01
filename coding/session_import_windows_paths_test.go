package coding

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// Pi 1be312b4 (#10488) agent-session-runtime.ts importFromJsonl: the self-check `pathsEqual(destinationPath, resolvedPath)` ignores case on Windows, so a session file
// that already lives in the session directory, spelled in other casing, is opened in place rather than copied beside itself.
func TestImportDestinationRecognizesAStoredSessionInOtherCasingOnWindows(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "Sessions")
	stored := filepath.Join(strings.ToLower(sessionDir), "s.jsonl")
	if _, already := importDestination(sessionDir, stored); already {
		t.Fatal("a source in other casing was already stored on a case-sensitive platform")
	}
	t.Cleanup(resolvepath.OverridePlatform("windows"))
	destination, already := importDestination(sessionDir, stored)
	if !already || destination != filepath.Join(sessionDir, "s.jsonl") {
		t.Fatalf("importDestination = %q, %v; want the stored session recognized", destination, already)
	}
}
