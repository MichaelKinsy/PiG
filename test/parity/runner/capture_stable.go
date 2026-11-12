//go:build parity

package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// defaultStepWaitTimeout is the time budget of a tmux step that sets no wait_timeout_seconds.
const defaultStepWaitTimeout = 5 * time.Second

// stepWaitTimeout returns the time budget of one tmux step's waits.
func stepWaitTimeout(step TmuxStep) time.Duration {
	if timeout := time.Duration(step.WaitTimeoutSeconds) * time.Second; timeout > 0 {
		return timeout
	}
	return defaultStepWaitTimeout
}

// finalCaptureBudget is the existing time budget the final capture may spend waiting for a still screen: the last step's own wait budget, or the ready timeout for a scenario without steps.
func finalCaptureBudget(cfg TmuxDriverConfig) time.Duration {
	if n := len(cfg.Steps); n > 0 {
		return stepWaitTimeout(cfg.Steps[n-1])
	}
	return time.Duration(cfg.ReadyTimeoutSeconds) * time.Second
}

// errScreenNeverStable marks a final capture that never found a still screen within its budget.
var errScreenNeverStable = errors.New("the screen never became stable")

// paneReader reads the pane once; escaped selects the capture that keeps the escape sequences.
type paneReader func(escaped bool) (string, error)

// captureStablePair returns the plain and the escaped capture of one screen that did not change while they were taken.
//
// The two captures are separate tmux commands, and tmux does not hold back a TUI's synchronized-output frame: Pi repaints every row of its fullscreen layout as one burst of "move to row, clear row, write row" when a bash block completes, so a capture taken inside the burst shows a mix of the old and the new frame. A plain capture before and after the escaped one tells the two apart: when they differ the screen moved while the pair was taken, and the pair is taken again, every poll, until the budget ends.
func captureStablePair(ctx context.Context, read paneReader, budget, poll time.Duration) (string, string, error) {
	deadline := time.Now().Add(budget)
	for {
		before, err := read(false)
		if err != nil {
			return "", "", err
		}
		escaped, err := read(true)
		if err != nil {
			return "", "", err
		}
		after, err := read(false)
		if err != nil {
			return "", "", err
		}
		if before == after {
			return before, escaped, nil
		}
		if !time.Now().Before(deadline) {
			return "", "", fmt.Errorf("%w within %s: the plain captures taken before and after the escaped capture differ (%s)", errScreenNeverStable, budget, paneDiffSummary(before, after))
		}
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(poll):
		}
	}
}

// paneDiffSummary says how two captures of one pane differ: how many lines, and the first differing line in both.
func paneDiffSummary(before, after string) string {
	beforeLines, afterLines := strings.Split(before, "\n"), strings.Split(after, "\n")
	total := max(len(beforeLines), len(afterLines))
	differing, first := 0, 0
	var firstBefore, firstAfter string
	for i := range total {
		var b, a string
		if i < len(beforeLines) {
			b = beforeLines[i]
		}
		if i < len(afterLines) {
			a = afterLines[i]
		}
		if a == b {
			continue
		}
		if differing == 0 {
			first, firstBefore, firstAfter = i+1, b, a
		}
		differing++
	}
	return fmt.Sprintf("%d of %d lines differ; first at line %d: %q, then %q", differing, total, first, firstBefore, firstAfter)
}

// cropCaptures crops the plain and the escaped capture to the scenario's capture markers. A capture_start that is not in a capture fails, naming the capture and the marker; the captures come back uncropped so the run's output still shows what the screen held.
func cropCaptures(cfg TmuxDriverConfig, plain, escaped string) (string, string, error) {
	if cfg.CaptureStart == "" && cfg.CaptureEnd == "" && cfg.CaptureEndRegex == "" {
		return plain, escaped, nil
	}
	croppedPlain, plainErr := cropCapture(plain, cfg.CaptureStart, cfg.CaptureEnd, cfg.CaptureEndRegex, cfg.CaptureStartLast)
	croppedEscaped, escapedErr := cropCapture(escaped, cfg.CaptureStart, cfg.CaptureEnd, cfg.CaptureEndRegex, cfg.CaptureStartLast)
	var errs []error
	if plainErr != nil {
		errs = append(errs, fmt.Errorf("plain capture: %w", plainErr))
	}
	if escapedErr != nil {
		errs = append(errs, fmt.Errorf("escaped capture: %w", escapedErr))
	}
	if len(errs) > 0 {
		return plain, escaped, errors.Join(errs...)
	}
	return croppedPlain, croppedEscaped, nil
}

// finalCaptureLabel names the point of a scenario at which the final capture is taken, for error messages.
func finalCaptureLabel(cfg TmuxDriverConfig) string {
	if n := len(cfg.Steps); n > 0 {
		return fmt.Sprintf("final capture after step %d of %d", n, n)
	}
	return "final capture of a scenario without steps"
}
