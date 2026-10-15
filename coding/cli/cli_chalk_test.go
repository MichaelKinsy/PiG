package cli

import (
	"bytes"
	"testing"
)

// Upstream prints CLI errors and warnings with chalk.red / chalk.yellow from the default chalk instance (main.ts, model-resolver.ts), whose
// applyStyle closes and re-opens the color around each line break. formatNoModelsAvailableMessage and the extension load hint span several lines.
// PiG wrapped the whole text once, so a terminal that resets attributes per line lost the color after the first line.
func TestCLIErrorsAndWarningsStyleEachLineLikeChalk(t *testing.T) {
	var stderr bytes.Buffer
	writeCLIError(&stderr, "first\nsecond", true)
	if want := "\x1b[31mError: first\x1b[39m\n\x1b[31msecond\x1b[39m\n"; stderr.String() != want {
		t.Fatalf("error = %q, want %q", stderr.String(), want)
	}
	stderr.Reset()
	reportArgDiagnostics(&stderr, []argDiagnostic{{Type: "warning", Message: "one\r\ntwo"}}, true)
	if want := "\x1b[33mWarning: one\x1b[39m\r\n\x1b[33mtwo\x1b[39m\n"; stderr.String() != want {
		t.Fatalf("warning = %q, want %q", stderr.String(), want)
	}
	if got := formatSessionCreationWarning("id", true); got != "\x1b[33mWarning: No project session found with id 'id'; creating a new session with that id.\x1b[39m" {
		t.Fatalf("session warning = %q", got)
	}
}
