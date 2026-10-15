package subprocess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func TestStderrCausePicksTheRuntimeErrorLine(t *testing.T) {
	cases := map[string]struct{ log, want string }{
		"node syntax": {"/x/broken.ts:2\n  foo(, )\n      ^\n\nSyntaxError [ERR_INVALID_TYPESCRIPT_SYNTAX]: Expression expected\n    at parseTypeScript (node:internal:72:36)\n\nNode.js v24.14.1\n", "SyntaxError [ERR_INVALID_TYPESCRIPT_SYNTAX]: Expression expected"},
		"node throw":  {"Error: register boom\n    at default (file:///x.mjs:1:36)\n", "Error: register boom"},
		"python":      {"Traceback (most recent call last):\n  File \"x.py\", line 1, in <module>\nValueError: bad value\n", "ValueError: bad value"},
		"go panic":    {"panic: nil map write\n\ngoroutine 1 [running]:\nmain.main()\n\t/x/main.go:5 +0x1\nexit status 2\n", "panic: nil map write"},
		"rust panic":  {"thread 'main' panicked at src/lib.rs:3:5:\nboom\nnote: run with `RUST_BACKTRACE=1`\n", "thread 'main' panicked at src/lib.rs:3:5: boom"},
		// Rust 1.89 and later print the thread id after the name.
		"rust panic with thread id": {"\nthread '<unnamed>' (368503) panicked at src/lib.rs:1:44:\nTool \"bad\" must define an object parameter schema\nnote: run with `RUST_BACKTRACE=1` environment variable to display a backtrace\nscenario-driver-rust: extension panicked\n", "thread '<unnamed>' (368503) panicked at src/lib.rs:1:44: Tool \"bad\" must define an object parameter schema"},
		"fallback":                  {"could not start\n    at somewhere\n", "could not start"},
		"empty":                     {"", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "stderr.log")
			if err := os.WriteFile(path, []byte(tc.log), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := stderrCause(path, ""); got != tc.want {
				t.Fatalf("stderrCause = %q, want %q", got, tc.want)
			}
		})
	}
	if got := stderrCause(filepath.Join(t.TempDir(), "missing.log"), ""); got != "" {
		t.Fatalf("missing log cause = %q", got)
	}
	long := filepath.Join(t.TempDir(), "long.log")
	if err := os.WriteFile(long, []byte(strings.Repeat("x", stderrCauseLimit*2)+"\nError: tail cause\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := stderrCause(long, ""); got != "Error: tail cause" {
		t.Fatalf("large log cause = %q", got)
	}
}

// A tail that is one line as long as stderrCauseLimit is still read: bufio.Scanner, bounded at the same limit, stopped
// with ErrTooLong before the line and reported no cause.
func TestStderrCauseReadsALineAsLongAsTheTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "line.log")
	if err := os.WriteFile(path, []byte("Error: "+strings.Repeat("x", stderrCauseLimit-len("Error: "))), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := stderrCause(path, ""); !strings.HasPrefix(got, "Error: xxx") {
		t.Fatalf("cause = %.40q, want the error line", got)
	}
}

// A cause longer than the limit is cut at characters, not bytes: a byte cut inside a multi-byte character left invalid UTF-8.
func TestStderrCauseCutsAtCharactersNotBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stderr.log")
	long := strings.Repeat("é", 600)
	if err := os.WriteFile(path, []byte(`extension "x" failed to load: Error: `+long+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := stderrCause(path, "x")
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "…") || jsstring.Length(got) != maxStderrCauseLength+1 {
		t.Fatalf("cause = %q (%d UTF-16 units), want %d valid characters and an ellipsis", got, jsstring.Length(got), maxStderrCauseLength)
	}
}

// The cut counts UTF-16 units. A cut between the halves of a surrogate pair writes U+FFFD, as Node writes a lone surrogate.
func TestStderrCauseCutInsideASurrogatePairWritesTheReplacementCharacter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stderr.log")
	if err := os.WriteFile(path, []byte(`extension "x" failed to load: Error: `+strings.Repeat("😀", 400)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := stderrCause(path, "x")
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "\uFFFD…") {
		t.Fatalf("cause = %q, want valid UTF-8 ending in U+FFFD and an ellipsis", got)
	}
}
