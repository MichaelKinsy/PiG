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

// captureStablePair returns the plain and the escaped capture of one screen that did not change while they were taken and that holds what the scenario crops to.
//
// The two captures are separate tmux commands, and tmux does not hold back a TUI's synchronized-output frame: Pi repaints every row of its fullscreen layout as one burst of "move to row, clear row, write row" when a bash block completes, so a capture taken inside the burst shows a mix of the old and the new frame. A plain capture before and after the escaped one tells a moving screen from a still one: when they differ the pair is taken again, every poll, until the budget ends.
//
// A starved writer can also stop in the middle of a burst for longer than a capture takes, and the screen then holds still with rows missing. complete says whether a still pair holds what the scenario needs (its capture_start); a still pair that does not is taken again as well. When the budget ends on a still pair that is not complete, that pair is returned so the caller's crop names what is missing; when it ends on a moving screen the error says so.
func captureStablePair(ctx context.Context, read paneReader, complete func(plain, escaped string) bool, budget, poll time.Duration) (string, string, error) {
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
		still := before == after
		if still && (complete == nil || complete(before, escaped)) {
			return before, escaped, nil
		}
		if !time.Now().Before(deadline) {
			if still {
				return before, escaped, nil
			}
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

// captureUse says which of a scenario's final captures a comparator reads.
type captureUse struct{ plain, escaped bool }

// scenarioCaptureUse derives from the scenario's assertions which capture kinds are compared. The escaped capture is read by escaped_output_equal only. The plain capture is read by the output comparators, the contains and regex assertions (a divergence block's contains lists included), the JSON comparator and the changelog header check.
func scenarioCaptureUse(sc *Scenario) captureUse {
	a := sc.Assert
	plain := a.OutputEqual || a.OutputNormalizedEqual || a.OutputLayoutEqual || a.JSONOutputEqual || a.ChangelogHeadersComplete ||
		len(a.BothContain)+len(a.BothNotContain)+len(a.PigContains)+len(a.PigNotContain)+len(a.PiContains)+len(a.PiNotContain)+len(a.BothMatchRegex) > 0 ||
		len(sc.Diverge.PigContains)+len(sc.Diverge.PiContains) > 0
	return captureUse{plain: plain, escaped: a.EscapedOutputEqual}
}

// cropsCaptures reports whether the scenario configures a crop at all.
func (cfg TmuxDriverConfig) cropsCaptures() bool {
	return cfg.CaptureStart != "" || cfg.CaptureEnd != "" || cfg.CaptureEndRegex != ""
}

// cropCaptures crops the captures a comparator reads to the scenario's capture markers. A capture_start that is not in such a capture fails, naming the capture and the marker; the captures come back uncropped so the run's artifacts still show what the screen held. A capture no comparator reads is not cropped: a missing marker there cannot change any verdict, and the capture is kept whole (the driver marks it uncropped in the artifact name) rather than cropped to something else.
func cropCaptures(cfg TmuxDriverConfig, use captureUse, plain, escaped string) (string, string, error) {
	if !cfg.cropsCaptures() {
		return plain, escaped, nil
	}
	var errs []error
	croppedPlain, croppedEscaped := plain, escaped
	if use.plain {
		var err error
		if croppedPlain, err = cropCapture(plain, cfg.CaptureStart, cfg.CaptureEnd, cfg.CaptureEndRegex, cfg.CaptureStartLast); err != nil {
			errs = append(errs, fmt.Errorf("plain capture: %w", err))
		}
	}
	if use.escaped {
		var err error
		if croppedEscaped, err = cropCapture(escaped, cfg.CaptureStart, cfg.CaptureEnd, cfg.CaptureEndRegex, cfg.CaptureStartLast); err != nil {
			errs = append(errs, fmt.Errorf("escaped capture: %w", err))
		}
	}
	if len(errs) > 0 {
		return plain, escaped, errors.Join(errs...)
	}
	return croppedPlain, croppedEscaped, nil
}

// uncroppedKinds says which captures cropCaptures left whole because no comparator reads them while the scenario crops the others.
func uncroppedKinds(cfg TmuxDriverConfig, use captureUse) (plain, escaped bool) {
	if !cfg.cropsCaptures() {
		return false, false
	}
	return !use.plain, !use.escaped
}

// finalCaptureError names the scenario, the binary and the final capture in a failure of that capture.
func finalCaptureError(scenario, binary string, cfg TmuxDriverConfig, err error) error {
	return fmt.Errorf("scenario %s (%s), %s: %w", scenario, binary, finalCaptureLabel(cfg), err)
}

// captureArtifactName is the artifact file name of a capture: the plain name, or one that says the capture is not asserted and not cropped.
func captureArtifactName(name string, uncropped bool) string {
	if uncropped {
		return name + ".not-asserted-uncropped"
	}
	return name
}

// finalCaptureLabel names the point of a scenario at which the final capture is taken, for error messages.
func finalCaptureLabel(cfg TmuxDriverConfig) string {
	if n := len(cfg.Steps); n > 0 {
		return fmt.Sprintf("final capture after step %d of %d", n, n)
	}
	return "final capture of a scenario without steps"
}
