package codemode_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
)

// PiG-only: packages/codemode/test only checks that an oversized write fails, with values far from the limits. These
// tests pin the exported limits to the boundary the prelude enforces and to the literals it embeds.
func TestPreludeEmbedsTheExportedLimits(t *testing.T) {
	for _, literal := range []string{
		fmt.Sprintf("outputChars > %d || outputItems > %d", codemode.MaxOutputChars, codemode.MaxOutputItems),
		fmt.Sprintf("json.length > %d", codemode.MaxStoreValueChars),
		fmt.Sprintf("next > %d", codemode.MaxStoreTotalChars),
	} {
		if !strings.Contains(codemode.PreludeSource, literal) {
			t.Errorf("prelude does not contain %q", literal)
		}
	}
}

func TestStoreLimitsAreInclusiveAtTheExportedValues(t *testing.T) {
	// The JSON of a string adds two quotes, so the longest storable string is MaxStoreValueChars-2 characters, and each
	// entry counts its key's length too.
	fit := codemode.MaxStoreValueChars - 2
	result := run(t, newSandbox(t, 30_000), fmt.Sprintf(`
		const attempt = (fn) => { try { fn(); return "ok"; } catch (error) { return error.name; } };
		const longest = attempt(() => store("a", "x".repeat(%[1]d)));
		const tooLong = attempt(() => store("b", "x".repeat(%[2]d)));
		store("a", undefined);
		const fills = [];
		for (let i = 0; i < 3; i++) fills.push(attempt(() => store("k" + i, "x".repeat(%[1]d))));
		// 3 entries use 3 * (2 + %[3]d); the last key "z" and its two quotes take the rest of the total exactly.
		const remaining = %[4]d - 3 * (2 + %[3]d) - 1 - 2;
		const exact = attempt(() => store("z", "x".repeat(remaining)));
		const overflow = attempt(() => store("o", ""));
		return [longest, tooLong, fills.join(","), exact, overflow];
	`, fit, fit+1, codemode.MaxStoreValueChars, codemode.MaxStoreTotalChars))
	wantOK(t, result, `["ok","RangeError","ok,ok,ok","ok","RangeError"]`)
}
