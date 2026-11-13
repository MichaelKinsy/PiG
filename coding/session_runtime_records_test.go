package coding

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func recordRuntimeOriginal(t *testing.T, site int) {
	t.Helper()
	if os.Getenv("PIG_RUNTIME_REPLACEMENT_PROBE") == "" {
		return
	}
	titles := map[int]string{
		126: "persists message_end assistant replacements to the session manager",
		167: "settles the active response before session replacement",
		218: "leaves no copy behind when an import fails on a missing session cwd",
		242: "preserves an existing session when importing a file with the same name",
		275: "emits session_before_switch and session_start for new and resume flows",
		320: "honors session_before_switch cancellation for new and resume",
		355: "emits session_before_fork and session_start and honors cancellation",
		404: "reports why an unflushed session cannot be forked",
		417: "duplicates the current active branch when forking at the current position",
		457: "duplicates the current active branch in-memory when forking at the current position",
		569: "throws when forking with an invalid entry id",
		574: "updates the runtime session cwd on cross-cwd session replacement",
		646: "restores model and thinking state from the destination session",
	}
	title, exists := titles[site]
	if !exists {
		t.Fatalf("unknown original case site %d", site)
	}
	t.Cleanup(func() {
		if t.Failed() {
			return
		}
		data, err := json.Marshal([]any{"runtime", site, title})
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("RUNTIME_ORIGINAL " + string(data))
	})
}
