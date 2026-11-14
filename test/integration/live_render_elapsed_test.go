//go:build integration

package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// elapsedStep is the footer value that shows from at on.
type elapsedStep struct {
	at    time.Duration
	value string
}

// elapsedSamples is the pane text of a running tool sampled every liveToolSampleInterval for window: the footer shows the value
// of the latest step that has begun.
func elapsedSamples(window time.Duration, shown []elapsedStep) []string {
	var samples []string
	for at := time.Duration(0); at <= window; at += liveToolSampleInterval {
		value := shown[0].value
		for _, step := range shown {
			if step.at <= at {
				value = step.value
			}
		}
		samples = append(samples, "Elapsed "+value+"s")
	}
	return samples
}

// TestElapsedAdvanceChecksTheTimerPiHas pins the oracle of TestLiveRenderingAdvancesWithoutInput: the one-second timer of Pi 1.5.2
// passes, a tenth-second repaint passes, and a footer that stopped fails.
func TestElapsedAdvanceChecksTheTimerPiHas(t *testing.T) {
	piTimer := []elapsedStep{{0, "0.3"}, {650 * time.Millisecond, "1.0"}, {1670 * time.Millisecond, "2.0"}, {2640 * time.Millisecond, "3.0"}, {3670 * time.Millisecond, "4.0"}}
	var tenths []elapsedStep
	for at := time.Duration(0); at <= 4*time.Second; at += 100 * time.Millisecond {
		tenths = append(tenths, elapsedStep{at, fmt.Sprintf("%.1f", 0.3+at.Seconds())})
	}
	stoppedAtOne := []elapsedStep{{0, "0.3"}, {650 * time.Millisecond, "1.0"}}
	stalledAfterOne := []elapsedStep{{0, "0.3"}, {650 * time.Millisecond, "1.0"}, {3000 * time.Millisecond, "3.4"}}
	lateFirstChange := []elapsedStep{{0, "0.3"}, {2300 * time.Millisecond, "2.6"}, {3700 * time.Millisecond, "4.0"}}
	for _, tc := range []struct {
		name    string
		shown   []elapsedStep
		wantErr string
	}{
		{"pi one second timer", piTimer, ""},
		{"repaint every tenth", tenths, ""},
		{"stopped after one second", stoppedAtOne, "advanced 0.7s"},
		{"stalled for two seconds and a third", stalledAfterOne, "did not advance within 2s"},
		{"first change later than the window", lateFirstChange, "did not advance within 2s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			samples := elapsedSamples(4*time.Second, tc.shown)
			err := elapsedAdvanceError(samples, 3*time.Second, elapsedRepaintWindow, liveToolSampleInterval)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
	if err := elapsedAdvanceError([]string{"Working"}, 0, elapsedRepaintWindow, liveToolSampleInterval); err == nil || !strings.Contains(err.Error(), "no elapsed footer") {
		t.Fatalf("a sample without the footer = %v", err)
	}
}
