package codemode_test

// Pins host.ts's timeout message (host.ts:149, `Execution timed out after ${timeoutMs} ms`) and the store limits of
// prelude-source.ts (MAX_STORE_VALUE_CHARS 256 * 1024, MAX_STORE_TOTAL_CHARS 1024 * 1024), which sandbox.test.ts reaches
// only with values far over a limit.

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
)

func TestTimeoutMessageNamesTheDeadlineInMilliseconds(t *testing.T) {
	e := wantFailure(t, run(t, newSandbox(t, 200), "while (true) {}"), codemode.ErrorTimeout)
	if e.Message != "Execution timed out after 200 ms" {
		t.Fatalf("message = %q", e.Message)
	}
	e = wantFailure(t, run(t, newSandbox(t, 10_000), "while (true) {}", codemode.ExecuteOptions{TimeoutMs: 150}), codemode.ErrorTimeout)
	if e.Message != "Execution timed out after 150 ms" {
		t.Fatalf("per-execution message = %q", e.Message)
	}
}

func TestStoreLimitsAreTheExportedConstantsAndApplyAtTheBoundary(t *testing.T) {
	if codemode.MaxStoreValueChars != 262144 || codemode.MaxStoreTotalChars != 1048576 || codemode.MaxOutputChars != 16777216 || codemode.MaxOutputItems != 100000 {
		t.Fatalf("limits = %d, %d, %d, %d", codemode.MaxStoreValueChars, codemode.MaxStoreTotalChars, codemode.MaxOutputChars, codemode.MaxOutputItems)
	}
	sandbox := newSandbox(t, 30_000)
	// A string value of n characters is n+2 characters of JSON.
	fits := run(t, sandbox, `store("a", "x".repeat(`+itoa(codemode.MaxStoreValueChars-2)+`)); return "ok"`)
	if !fits.OK {
		t.Fatalf("a value of exactly %d JSON characters was rejected: %+v", codemode.MaxStoreValueChars, fits.Error)
	}
	over := run(t, sandbox, `store("a", "x".repeat(`+itoa(codemode.MaxStoreValueChars-1)+`))`)
	if over.OK || over.Error == nil || !strings.Contains(over.Error.Message, "more than the limit of 262144") {
		t.Fatalf("a value of %d JSON characters = %+v", codemode.MaxStoreValueChars+1, over)
	}
	// Four values of 262144 characters fill the store exactly; a fifth key does not fit.
	total := run(t, sandbox, `
		for (const key of ["a", "b", "c"]) store(key, "x".repeat(`+itoa(codemode.MaxStoreValueChars-2)+`));
		store("d", "x".repeat(`+itoa(codemode.MaxStoreValueChars-2-4)+`));
		return "ok"`)
	if !total.OK {
		t.Fatalf("a store of exactly the total limit was rejected: %+v", total.Error)
	}
	full := run(t, sandbox, `
		for (const key of ["a", "b", "c"]) store(key, "x".repeat(`+itoa(codemode.MaxStoreValueChars-2)+`));
		store("d", "x".repeat(`+itoa(codemode.MaxStoreValueChars-2)+`));`)
	if full.OK || full.Error == nil || !strings.Contains(full.Error.Message, "store is full") {
		t.Fatalf("a store over the total limit = %+v", full)
	}
}

func itoa(n int) string {
	digits := ""
	for ; n > 0; n /= 10 {
		digits = string(rune('0'+n%10)) + digits
	}
	if digits == "" {
		return "0"
	}
	return digits
}
