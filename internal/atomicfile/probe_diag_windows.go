//go:build windows

package atomicfile

import (
	"context"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/fsretry"
)

// ProbeWriteStats records retry statistics for a single diagnostic write.
type ProbeWriteStats struct {
	Retries         int
	RetryWait       time.Duration
	BudgetExhausted bool
}

// ProbeWriteFile writes data to path using one of three methods for the
// Windows replacement diagnostic probe:
//   - "R": the current ReplaceFileW + backup + descriptor carry path (replace_windows.go:52-69)
//   - "A1": descriptor carry onto temp + MoveFileEx(REPLACE_EXISTING|WRITE_THROUGH) (replace_windows.go:66-69)
//   - "A2": writeInPlace (atomicfile.go:109; Pi's writeFileSync form)
//
// Not concurrent-safe within one process because it swaps package-level replaceTemp;
// the probe runs one writer process against reader processes.
func ProbeWriteFile(method, path string, data []byte, perm os.FileMode) (ProbeWriteStats, error) {
	var stats ProbeWriteStats
	wait := func(ctx context.Context, delay time.Duration) error {
		stats.Retries++
		start := time.Now()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			stats.RetryWait += time.Since(start)
			return ctx.Err()
		case <-timer.C:
			stats.RetryWait += time.Since(start)
			return nil
		}
	}

	switch method {
	case "A2":
		err := writeInPlace(path, data, perm, nil)
		return stats, err
	case "R":
		prev := replaceTemp
		replaceTemp = func(temp, target string, existed bool) error {
			return probeReplaceR(temp, target, existed, wait)
		}
		defer func() { replaceTemp = prev }()
		err := WriteFile(path, data, perm)
		if err != nil && fsretry.TransientRename(err) {
			stats.BudgetExhausted = true
		}
		return stats, err
	case "A1":
		prev := replaceTemp
		replaceTemp = func(temp, target string, existed bool) error {
			return probeReplaceA1(temp, target, existed, wait)
		}
		defer func() { replaceTemp = prev }()
		err := WriteFile(path, data, perm)
		if err != nil && fsretry.TransientRename(err) {
			stats.BudgetExhausted = true
		}
		return stats, err
	default:
		return stats, errors.New("unknown probe method: " + method)
	}
}

// probeReplaceR mirrors replace_windows.go:46-70 identically while routing
// fsretry waits through wait so retry counts and durations are recorded.
func probeReplaceR(temp, path string, existed bool, wait func(context.Context, time.Duration) error) error {
	ctx := context.Background()
	if !existed {
		return fsretry.Retry(ctx, wait, func() error { return moveFile(temp, path) })
	}
	err := replaceKeepingBackup(path, temp, replaceSteps{
		replaceFile: func(replaced, replacement, backup string) error {
			return fsretry.Retry(ctx, wait, func() error { return replaceFile(replaced, replacement, backup) })
		},
		moveFile: func(from, to string) error {
			return fsretry.Retry(ctx, wait, func() error { return moveFile(from, to) })
		},
		carry: carryOnto(path),
	})
	if !errors.Is(err, windows.ERROR_INVALID_FUNCTION) && !errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
		return err
	}
	if err := carryDescriptor(path, temp, false); err != nil && !errors.Is(err, errAutoInheritedDiffers) {
		return err
	}
	return fsretry.Retry(ctx, wait, func() error { return moveFile(temp, path) })
}

// probeReplaceA1 executes the fallback branch at replace_windows.go:66-69 directly:
// carry the existing file's descriptor onto temp, then MoveFileEx with
// MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH retried under fsretry.
func probeReplaceA1(temp, path string, existed bool, wait func(context.Context, time.Duration) error) error {
	ctx := context.Background()
	if existed {
		if err := carryDescriptor(path, temp, false); err != nil && !errors.Is(err, errAutoInheritedDiffers) {
			return err
		}
	}
	return fsretry.Retry(ctx, wait, func() error { return moveFile(temp, path) })
}
