package runtimecell

import (
	"errors"
	"fmt"
	"os/exec"
)

// toolchains names what each language extension needs on PATH, and where to get
// it. A package installed from a marketplace carries source for its language,
// so the first thing a user on a clean machine hits is the compiler being
// absent. exec reports that as `exec: "go": executable file not found in $PATH`,
// which names neither the requirement nor the remedy.
var toolchains = map[string]struct{ binary, install string }{
	"go":   {"go", "run `pig setup go` to install a verified Go toolchain, or install Go 1.26 or newer from https://go.dev/dl/"},
	"rust": {"cargo", "install Rust from https://rustup.rs (or via mise/asdf/brew)"},
}

// explainMissingToolchain converts a failure to launch a language toolchain
// into an error that says what is missing and how to supply it. It reports
// false for anything else, so a real compile failure keeps its own diagnostics
// rather than being buried under toolchain advice.
//
// The alternative to installing a toolchain is a package that ships prebuilt
// cells, which is why that route is named here: it is the only way to run a
// compiled extension on a machine that will never have a compiler.
func explainMissingToolchain(lang string, err error) (error, bool) {
	if err == nil || !errors.Is(err, exec.ErrNotFound) {
		return err, false
	}
	spec, ok := toolchains[lang]
	if !ok {
		return err, false
	}
	return fmt.Errorf(
		"%s extensions are compiled on this machine and %q is not on PATH.\n"+
			"Either %s, or install a package that ships prebuilt cells, which needs no toolchain.\n"+
			"underlying error: %w",
		lang, spec.binary, spec.install, err), true
}
