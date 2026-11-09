package extensionconformance

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// readMarker is a file the extension wrote from its handler, "" until it exists. The handlers report through files: once the signal aborted, a host call of the handler is cancelled with its request, so a notification would hide exactly the delivery the test looks for.
func readMarker(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// awaitMarker waits for the marker file, failing early when the operation under test already returned.
func awaitMarker(t *testing.T, path string, returned <-chan struct{}) {
	t.Helper()
	deadline := time.After(testbudget.Wait(t))
	for readMarker(path) == "" {
		select {
		case <-returned:
			t.Fatalf("the operation returned before %s was written", filepath.Base(path))
		case <-deadline:
			t.Fatalf("%s was never written", filepath.Base(path))
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Pi awaits runner.emit({ type: "session_before_compact", signal }) with every handler, in order, and the signal is what the abort reaches (agent-session.ts:2878-2890, 3206-3218; runner.ts emit takes no signal): an extension after the one running when the compaction is aborted still receives the event, with event.signal already aborted.
// The first extension blocks in its handler until its own signal aborts and then returns; strict runs each extension in its own process, shared-ok packs them into one cell.
func TestNodeSessionBeforeCompactReachesTheNextExtensionWhenAbortedDuringDispatch(t *testing.T) {
	eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
		dir := t.TempDir()
		entered, firstAborted, second := filepath.Join(dir, "entered"), filepath.Join(dir, "first"), filepath.Join(dir, "second")
		rig := newNodeSessionRig(t, isolation, `"compaction":{"keepRecentTokens":1}`, []ai.FauxModelDefinition{{ID: "faux-1"}},
			nodeAPIFixture{"before-compact-first", `import { writeFileSync } from "node:fs";
export default function (pi) {
  pi.on("session_before_compact", async (event) => {
    writeFileSync(` + strconv.Quote(entered) + `, "1");
    await new Promise((resolve) => event.signal.aborted ? resolve() : event.signal.addEventListener("abort", resolve, { once: true }));
    writeFileSync(` + strconv.Quote(firstAborted) + `, String(event.signal.aborted));
  });
}
`},
			nodeAPIFixture{"before-compact-second", `import { writeFileSync } from "node:fs";
export default function (pi) {
  pi.on("session_before_compact", async (event) => {
    writeFileSync(` + strconv.Quote(second) + `, String(event.signal.aborted));
  });
}
`})
		rig.faux.SetResponses([]ai.FauxResponseStep{fauxTextStep("one"), fauxTextStep("two"), fauxTextStep("three")})
		for _, text := range []string{"a", "b", "c"} {
			if err := rig.session.Prompt(t.Context(), text); err != nil {
				t.Fatal(err)
			}
		}

		done := make(chan error, 1)
		returned := make(chan struct{})
		go func() {
			_, err := rig.session.Compact(t.Context(), "")
			done <- err
			close(returned)
		}()
		awaitMarker(t, entered, returned)
		rig.session.AbortCompaction()
		awaitMarker(t, second, returned)
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("Compact succeeded; the compaction was aborted before its summary")
			}
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("Compact did not return after the abort")
		}
		if got := readMarker(firstAborted); got != "true" {
			t.Fatalf("the first extension saw event.signal.aborted = %q after the abort, want true", got)
		}
		if got := readMarker(second); got != "true" {
			t.Fatalf("the second extension saw event.signal.aborted = %q, want true on its first line", got)
		}
	})
}

// Pi awaits runner.emit({ type: "session_before_tree", signal }) with every handler and checks no abort after it (agent-session.ts:4107-4135): a cancel or a summary an extension returns after the navigation was aborted is honoured.
// The first extension blocks until its signal aborts; the second returns its result after the abort, and its handler starts with event.signal already aborted.
func TestNodeSessionBeforeTreeHonoursAResultReturnedAfterTheAbort(t *testing.T) {
	for _, mode := range []string{"cancel", "summary"} {
		t.Run(mode, func(t *testing.T) {
			eachNodeAPIIsolation(t, func(t *testing.T, isolation string) {
				dir := t.TempDir()
				entered, second := filepath.Join(dir, "entered"), filepath.Join(dir, "second")
				result := `{ cancel: true }`
				if mode == "summary" {
					result = `{ summary: { summary: "late extension summary" } }`
				}
				rig := newNodeSessionRig(t, isolation, "", []ai.FauxModelDefinition{{ID: "faux-1"}},
					nodeAPIFixture{"before-tree-first", `import { writeFileSync } from "node:fs";
export default function (pi) {
  pi.on("session_before_tree", async (event) => {
    writeFileSync(` + strconv.Quote(entered) + `, "1");
    await new Promise((resolve) => event.signal.aborted ? resolve() : event.signal.addEventListener("abort", resolve, { once: true }));
  });
}
`},
					nodeAPIFixture{"before-tree-second", `import { writeFileSync } from "node:fs";
export default function (pi) {
  pi.on("session_before_tree", async (event) => {
    writeFileSync(` + strconv.Quote(second) + `, String(event.signal.aborted));
    return ` + result + `;
  });
}
`})
				rig.faux.SetResponses([]ai.FauxResponseStep{fauxTextStep("one"), fauxTextStep("two")})
				for _, text := range []string{"a", "b"} {
					if err := rig.session.Prompt(t.Context(), text); err != nil {
						t.Fatal(err)
					}
				}
				var target string
				for _, entry := range rig.session.Entries() {
					if entry.Base().Type == "message" {
						target = entry.Base().ID
						break
					}
				}
				leaf := rig.session.LeafID()
				if target == "" || leaf == nil || *leaf == target {
					t.Fatalf("no entry to navigate to: target %q, leaf %v", target, leaf)
				}

				type outcome struct {
					result coding.NavigateTreeResult
					err    error
				}
				done := make(chan outcome, 1)
				returned := make(chan struct{})
				go func() {
					result, err := rig.session.NavigateTree(t.Context(), target, coding.NavigateTreeOptions{Summarize: mode == "summary"})
					done <- outcome{result, err}
					close(returned)
				}()
				awaitMarker(t, entered, returned)
				rig.session.AbortBranchSummary()
				var got outcome
				select {
				case got = <-done:
				case <-time.After(testbudget.Wait(t)):
					t.Fatal("NavigateTree did not return after the abort")
				}
				if got.err != nil {
					t.Fatalf("NavigateTree: %v", got.err)
				}
				if marker := readMarker(second); marker != "true" {
					t.Fatalf("the second extension saw event.signal.aborted = %q, want true on its first line", marker)
				}
				switch mode {
				case "cancel":
					if !got.result.Cancelled {
						t.Fatalf("NavigateTree = %+v, want the late {cancel:true} honoured", got.result)
					}
					if now := rig.session.LeafID(); now == nil || *now != *leaf {
						t.Fatalf("leaf moved to %v despite the cancel; want %s", now, *leaf)
					}
				case "summary":
					if got.result.Cancelled || got.result.SummaryEntry == nil || got.result.SummaryEntry.Summary != "late extension summary" {
						t.Fatalf("NavigateTree = %+v, want the late extension summary saved", got.result)
					}
				}
			})
		})
	}
}
