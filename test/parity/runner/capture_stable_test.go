//go:build parity

package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakePane serves scripted screens: each read returns the next entry, and the last entry repeats.
type fakePane struct {
	screens []string
	reads   int
}

func (p *fakePane) read(bool) (string, error) {
	i := min(p.reads, len(p.screens)-1)
	p.reads++
	return p.screens[i], nil
}

func TestCaptureStablePair_TakesAnUnstablePairAgain(t *testing.T) {
	// Round one: the plain captures around the escaped one differ (the screen moved). Round two: all still.
	pane := &fakePane{screens: []string{"old frame", "escaped one", "new frame", "new frame", "escaped two", "new frame"}}
	plain, escaped, err := captureStablePair(context.Background(), pane.read, nil, time.Minute, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "new frame" || escaped != "escaped two" {
		t.Fatalf("got plain=%q escaped=%q, want the second round's captures", plain, escaped)
	}
	if pane.reads != 6 {
		t.Fatalf("read the pane %d times, want 6 (one extra round of three captures)", pane.reads)
	}
}

func TestCaptureStablePair_StableScreenIsOneRound(t *testing.T) {
	pane := &fakePane{screens: []string{"still", "escaped", "still"}}
	plain, escaped, err := captureStablePair(context.Background(), pane.read, nil, time.Minute, time.Millisecond)
	if err != nil || plain != "still" || escaped != "escaped" || pane.reads != 3 {
		t.Fatalf("plain=%q escaped=%q reads=%d err=%v, want one round of three captures", plain, escaped, pane.reads, err)
	}
}

func TestCaptureStablePair_NeverStableFailsWithTheDiff(t *testing.T) {
	flip := 0
	read := func(bool) (string, error) {
		flip++
		// Within every round of three reads the first and the third differ.
		return "row one\nrow two " + strings.Repeat("x", flip/3), nil
	}
	_, _, err := captureStablePair(context.Background(), read, nil, 30*time.Millisecond, time.Millisecond)
	if err == nil {
		t.Fatal("a screen that never stops changing was accepted")
	}
	for _, want := range []string{"never became stable", "30ms", "1 of 2 lines differ", "first at line 2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
}

func hasStart(plain, escaped string) bool {
	return strings.Contains(plain, "BEGIN") && strings.Contains(escaped, "BEGIN")
}

func TestCaptureStablePair_StillButIncompletePairIsTakenAgain(t *testing.T) {
	// Round one holds still but lacks the start row (a writer stopped mid-burst); round two is whole.
	pane := &fakePane{screens: []string{"banner", "esc banner", "banner", "BEGIN\nbody", "esc BEGIN\nbody", "BEGIN\nbody"}}
	plain, escaped, err := captureStablePair(context.Background(), pane.read, hasStart, time.Minute, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if plain != "BEGIN\nbody" || escaped != "esc BEGIN\nbody" || pane.reads != 6 {
		t.Fatalf("plain=%q escaped=%q reads=%d, want the second round's captures after 6 reads", plain, escaped, pane.reads)
	}
}

func TestCaptureStablePair_IncompleteToTheEndReturnsThePairForTheCropToFail(t *testing.T) {
	pane := &fakePane{screens: []string{"banner", "esc banner", "banner"}}
	plain, escaped, err := captureStablePair(context.Background(), pane.read, hasStart, 20*time.Millisecond, time.Millisecond)
	if err != nil || plain != "banner" || escaped != "banner" {
		// The fake repeats its last screen, so the last round's escaped capture reads "banner".
		t.Fatalf("plain=%q escaped=%q err=%v, want the still pair back without an error", plain, escaped, err)
	}
	cfg := TmuxDriverConfig{CaptureStart: "BEGIN"}
	if _, _, err := cropCaptures(cfg, plain, escaped); err == nil || !strings.Contains(err.Error(), `"BEGIN" not found`) {
		t.Fatalf("the crop must fail on the pair that never held its start, got %v", err)
	}
}

func TestCaptureStablePair_ReadErrorIsReturned(t *testing.T) {
	boom := errors.New("tmux gone")
	_, _, err := captureStablePair(context.Background(), func(bool) (string, error) { return "", boom }, nil, time.Minute, time.Millisecond)
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want the read error", err)
	}
}

func TestCaptureStablePair_CancelledContextStopsTheWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	flip := 0
	read := func(bool) (string, error) { flip++; return strings.Repeat("x", flip), nil }
	_, _, err := captureStablePair(ctx, read, nil, time.Minute, time.Hour)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestFinalCaptureBudget(t *testing.T) {
	tests := []struct {
		name string
		cfg  TmuxDriverConfig
		want time.Duration
	}{
		{"last step budget", TmuxDriverConfig{Steps: []TmuxStep{{WaitTimeoutSeconds: 15}, {WaitTimeoutSeconds: 7}}}, 7 * time.Second},
		{"step default", TmuxDriverConfig{Steps: []TmuxStep{{WaitTimeoutSeconds: 15}, {}}}, defaultStepWaitTimeout},
		{"no steps uses the ready timeout", TmuxDriverConfig{ReadyTimeoutSeconds: 120}, 120 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := finalCaptureBudget(tt.cfg); got != tt.want {
				t.Fatalf("budget %s, want %s", got, tt.want)
			}
		})
	}
}

func TestCropCapture_MissingStartIsAnError(t *testing.T) {
	if _, err := cropCapture("no marker here", "AUTH-JSON-1BEGIN", "", "", false); err == nil || !strings.Contains(err.Error(), `"AUTH-JSON-1BEGIN" not found`) {
		t.Fatalf("got %v, want an error naming the marker", err)
	}
	got, err := cropCapture("before\nAUTH-JSON-1BEGIN\nbody\nEND\nafter", "AUTH-JSON-1BEGIN", "END", "", false)
	if err != nil || got != "AUTH-JSON-1BEGIN\nbody\nEND" {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := cropCapture("tail only", "", "", "", false); err != nil || got != "tail only" {
		t.Fatalf("a crop without a start must not need one: %q, %v", got, err)
	}
}

func TestCropCaptures_MissingStartNamesTheCapture(t *testing.T) {
	cfg := TmuxDriverConfig{CaptureStart: "AUTH-JSON-1BEGIN", CaptureEnd: "AUTH-JSON-1END"}
	const withMarker = "banner\nAUTH-JSON-1BEGIN\n{}\nAUTH-JSON-1END"
	const without = "banner\nSaved API key"
	tests := []struct {
		name, plain, escaped string
		want                 []string
		notWant              []string
	}{
		{"escaped lacks the start", withMarker, without, []string{"escaped capture", `"AUTH-JSON-1BEGIN" not found`}, []string{"plain capture"}},
		{"plain lacks the start", without, withMarker, []string{"plain capture", `"AUTH-JSON-1BEGIN" not found`}, []string{"escaped capture"}},
		{"both lack the start", without, without, []string{"plain capture", "escaped capture"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plain, escaped, err := cropCaptures(cfg, tt.plain, tt.escaped)
			if err == nil {
				t.Fatal("a capture without capture_start was accepted")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q lacks %q", err, want)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(err.Error(), notWant) {
					t.Errorf("error %q wrongly names %q", err, notWant)
				}
			}
			if plain != tt.plain || escaped != tt.escaped {
				t.Errorf("captures must come back uncropped on failure: plain=%q escaped=%q", plain, escaped)
			}
		})
	}
	plain, escaped, err := cropCaptures(cfg, withMarker, withMarker)
	if err != nil || plain != "AUTH-JSON-1BEGIN\n{}\nAUTH-JSON-1END" || escaped != plain {
		t.Fatalf("both captures carry the start: plain=%q escaped=%q err=%v", plain, escaped, err)
	}
}

func TestFinalCaptureLabel(t *testing.T) {
	if got := finalCaptureLabel(TmuxDriverConfig{Steps: make([]TmuxStep, 3)}); got != "final capture after step 3 of 3" {
		t.Fatalf("got %q", got)
	}
	if got := finalCaptureLabel(TmuxDriverConfig{}); !strings.Contains(got, "without steps") {
		t.Fatalf("got %q", got)
	}
}
