package testenv

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime/pprof"
	"testing"
)

// LeakedGoroutines runs Go's goroutineleak detection and returns the number of goroutines blocked forever on a channel, mutex or other primitive that no
// live goroutine can reach, with the profile text that names their stacks. The detection runs only while the profile is written: [pprof.Profile.Count]
// alone reports the result of the last write, so a check that reads Count without writing first passes whatever leaked.
func LeakedGoroutines() (int, string, error) {
	profile := pprof.Lookup("goroutineleak")
	if profile == nil {
		return 0, "", fmt.Errorf("runtime has no goroutineleak profile")
	}
	var text bytes.Buffer
	if err := profile.WriteTo(&text, 1); err != nil {
		return 0, "", fmt.Errorf("write goroutineleak profile: %w", err)
	}
	return profile.Count(), text.String(), nil
}

// RunWithoutGoroutineLeaks runs the package's tests and then fails the test binary when goroutines leaked: TestMain calls
// os.Exit(testenv.RunWithoutGoroutineLeaks(m)). A failing test run returns its own code unchanged. Each afterTests
// function runs after the tests and before the check; a package uses one to end goroutines that deliberately model a
// crashed process, whose pending work Pi's runtime simply drops.
func RunWithoutGoroutineLeaks(m *testing.M, afterTests ...func()) int {
	return runWithoutGoroutineLeaks(m.Run, afterTests, os.Stderr)
}

func runWithoutGoroutineLeaks(run func() int, afterTests []func(), stderr io.Writer) int {
	code := run()
	for _, release := range afterTests {
		release()
	}
	if code != 0 {
		return code
	}
	leaked, text, err := LeakedGoroutines()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if leaked != 0 {
		_, _ = fmt.Fprintf(stderr, "FAIL: %d goroutine(s) leaked: blocked forever on a primitive no live goroutine can reach\n%s", leaked, text)
		return 1
	}
	return 0
}
