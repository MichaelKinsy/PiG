package runtimecell

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// A package installed from a marketplace carries source for its language, so
// the first thing a user on a clean machine hits is the compiler being absent.
// exec reports that as `exec: "go": executable file not found in $PATH`, which
// names neither the requirement nor the remedy, and pig then appended a dump of
// a generated go.mod the user did not write and cannot act on.
func TestMissingToolchainErrorNamesTheRequirementAndTheRemedy(t *testing.T) {
	for _, tc := range []struct {
		lang string
		want []string
	}{
		{"go", []string{"go", "https://go.dev/dl/", "prebuilt cells"}},
		{"rust", []string{"cargo", "https://rustup.rs", "prebuilt cells"}},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			raw := &exec.Error{Name: toolchains[tc.lang].binary, Err: exec.ErrNotFound}
			got, explained := explainMissingToolchain(tc.lang, raw)
			if !explained {
				t.Fatal("a missing toolchain was passed through unexplained")
			}
			for _, want := range tc.want {
				if !strings.Contains(got.Error(), want) {
					t.Errorf("message omits %q: %s", want, got)
				}
			}
			// The cause must survive so callers can still match on it.
			if !errors.Is(got, exec.ErrNotFound) {
				t.Error("explanation dropped the underlying exec.ErrNotFound")
			}
		})
	}
}

// A real compile failure must keep its own diagnostics. Rewriting every build
// error into toolchain advice would bury the compiler output that says what is
// actually wrong with the extension.
func TestCompileFailuresAreNotRewrittenAsToolchainAdvice(t *testing.T) {
	compileErr := fmt.Errorf("exit status 1")
	if got, explained := explainMissingToolchain("go", compileErr); explained {
		t.Errorf("a compile failure was rewritten as toolchain advice: %s", got)
	}
	if got, explained := explainMissingToolchain("go", nil); explained || got != nil {
		t.Errorf("nil error became %v (explained=%t)", got, explained)
	}
}

// A language with no recorded toolchain must not lose its error.
func TestUnknownLanguageKeepsItsError(t *testing.T) {
	raw := &exec.Error{Name: "tclsh", Err: exec.ErrNotFound}
	if got, explained := explainMissingToolchain("tcl", raw); explained {
		t.Errorf("unknown language rewrote the error: %s", got)
	}
}
