package subprocess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStderrCausePicksTheRuntimeErrorLine(t *testing.T) {
	cases := map[string]struct{ log, want string }{
		"node syntax": {"/x/broken.ts:2\n  foo(, )\n      ^\n\nSyntaxError [ERR_INVALID_TYPESCRIPT_SYNTAX]: Expression expected\n    at parseTypeScript (node:internal:72:36)\n\nNode.js v24.14.1\n", "SyntaxError [ERR_INVALID_TYPESCRIPT_SYNTAX]: Expression expected"},
		"node throw":  {"Error: register boom\n    at default (file:///x.mjs:1:36)\n", "Error: register boom"},
		"python":      {"Traceback (most recent call last):\n  File \"x.py\", line 1, in <module>\nValueError: bad value\n", "ValueError: bad value"},
		"go panic":    {"panic: nil map write\n\ngoroutine 1 [running]:\nmain.main()\n\t/x/main.go:5 +0x1\nexit status 2\n", "panic: nil map write"},
		"rust panic":  {"thread 'main' panicked at src/lib.rs:3:5:\nboom\nnote: run with `RUST_BACKTRACE=1`\n", "thread 'main' panicked at src/lib.rs:3:5:"},
		"fallback":    {"could not start\n    at somewhere\n", "could not start"},
		"empty":       {"", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "stderr.log")
			if err := os.WriteFile(path, []byte(tc.log), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := stderrCause(path); got != tc.want {
				t.Fatalf("stderrCause = %q, want %q", got, tc.want)
			}
		})
	}
	if got := stderrCause(filepath.Join(t.TempDir(), "missing.log")); got != "" {
		t.Fatalf("missing log cause = %q", got)
	}
	long := filepath.Join(t.TempDir(), "long.log")
	if err := os.WriteFile(long, []byte(strings.Repeat("x", stderrCauseLimit*2)+"\nError: tail cause\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := stderrCause(long); got != "Error: tail cause" {
		t.Fatalf("large log cause = %q", got)
	}
}
