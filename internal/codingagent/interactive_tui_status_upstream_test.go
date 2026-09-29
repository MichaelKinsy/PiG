package codingagent

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// .upstream/v0.87.1/packages/coding-agent/test/interactive-tui.test.ts:380
func TestInteractiveTuiStatusEditorOptInUpstream(t *testing.T) {
	for _, embedded := range []bool{true, false} {
		t.Run(fmt.Sprint(embedded), func(t *testing.T) {
			mode := &InteractiveMode{editor: tui.NewEditor(), statusContainer: tui.NewContainer(), opts: InteractiveOptions{TuiMode: "regular", Settings: Settings{ClearOnShrink: new(true)}}}
			mode.editor.EmbedWorkingStatus = embedded
			for _, item := range []struct{ kind, message string }{{"working", "Working"}, {"compaction", "Compacting context..."}, {"compaction", "Auto-compacting..."}, {"compaction", "Context overflow detected, Auto-compacting..."}, {"branchSummary", "Summarizing branch..."}, {"retry", "Retrying (1/3) in 1s..."}} {
				indicator := &tui.StatusIndicator{Kind: item.kind, Loader: tui.NewLoader(item.message)}
				mode.showStatusIndicator(indicator)
				if mode.activeStatusIndicator != indicator || mode.activeWorkingIndicatorEmbedded != embedded {
					t.Fatal("status did not retain indicator/opt-in identity")
				}
				if embedded {
					if mode.statusContainer.ChildCount() != 0 || !strings.Contains(strings.Join(mode.editor.Render(120), "\n"), item.message) {
						t.Fatalf("editor did not receive %s", item.message)
					}
				} else {
					_, child := mode.statusContainer.LastTwoChildren()
					if mode.statusContainer.ChildCount() != 1 || child != indicator {
						t.Fatal("standalone status did not receive indicator")
					}
				}
			}
			mode.clearStatusIndicator("")
		})
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/interactive-tui.test.ts:420
func TestInteractiveTuiEmbeddedStatusClearingUpstream(t *testing.T) {
	for _, kind := range []string{"working", "compaction", "branchSummary", "retry"} {
		t.Run(kind, func(t *testing.T) {
			disposed := 0
			indicator := &tui.StatusIndicator{Kind: kind, Loader: tui.NewLoader("UNIQUE_STATUS")}
			mode := statusBorderMode(t, true)
			mode.opts.TuiMode = "regular"
			mode.tuiInst.SetClearOnShrink(true)
			mode.editor.SetWorkingStatusIndicator(indicator)
			mode.activeStatusIndicator = indicator
			mode.activeWorkingIndicatorEmbedded = true
			// Pi's indicators own timers. PiG's owner advances the active indicator and owns the retry countdown; other kinds hold no independent resource to dispose.
			if kind == "retry" {
				mode.retryCountdownStop = func() { disposed++ }
			}
			mode.clearStatusIndicator("")
			mode.tickStatusIndicators(time.Now().Add(time.Second))
			if mode.activeStatusIndicator != nil || mode.activeWorkingIndicatorEmbedded || indicator.Frame != 0 || mode.statusContainer.ChildCount() != 0 || strings.Contains(strings.Join(mode.editor.Render(120), "\n"), "UNIQUE_STATUS") {
				t.Fatalf("cleared status retained state: active=%v embedded=%v frame=%d rows=%d", mode.activeStatusIndicator, mode.activeWorkingIndicatorEmbedded, indicator.Frame, mode.statusContainer.ChildCount())
			}
			mode.clearStatusIndicator("")
			if kind == "retry" && (disposed != 1 || mode.retryCountdownStop != nil) {
				t.Fatalf("retry cleanup calls=%d, want exactly one", disposed)
			}
		})
	}
}
