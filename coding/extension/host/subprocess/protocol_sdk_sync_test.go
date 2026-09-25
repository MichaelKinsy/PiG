package subprocess

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// evalProductExpr evaluates a constant expression of the form "128 * 1024 *
// 1024" or a plain integer literal, so the test compares numeric values rather
// than source text (a literal 134217728 in one SDK still matches the product
// form in another).
func evalProductExpr(t *testing.T, expr string) int {
	t.Helper()
	prod := 1
	for f := range strings.SplitSeq(expr, "*") {
		f = strings.TrimSpace(f)
		f = strings.TrimSuffix(f, "_u32") // tolerate a Rust suffix if ever added
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("cannot parse factor %q in %q: %v", f, expr, err)
		}
		prod *= n
	}
	return prod
}

// readFrameConst reads a single SDK source file and extracts its frame-cap
// constant via re (group 1 = the value expression). A missing file or no match
// is a hard failure: it means the SDK moved or the constant was renamed, which
// is exactly the drift this test guards.
func readFrameConst(t *testing.T, rel string, re *regexp.Regexp) int {
	t.Helper()
	path := filepath.Join("..", "..", "..", "..", filepath.FromSlash(rel))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read SDK source %s: %v (did the SDK layout change?)", rel, err)
	}
	m := re.FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("no frame-cap constant matching %q in %s (was it renamed?)", re, rel)
	}
	return evalProductExpr(t, m[1])
}

// TestMaxFrameSize_SDKsInSync enforces the contract that protocol.go only
// states in a comment: the host and all three SDK frame caps must be equal.
// A wire constant drifting unnoticed is the exact defect that silently broke
// extensions before; this fails the build the moment any one of them diverges.
func TestMaxFrameSize_SDKsInSync(t *testing.T) {
	cases := []struct {
		name string
		rel  string
		re   *regexp.Regexp
	}{
		{"go-sdk", "extensions/sdk/protocol.go", regexp.MustCompile(`(?m)^const MaxFrameSize\s*=\s*(.+)$`)},
		{"rust-sdk", "extensions/sdk-rs/src/protocol.rs", regexp.MustCompile(`MAX_FRAME_SIZE\s*:\s*u32\s*=\s*([^;]+);`)},
		{"py-sdk", "extensions/sdk-py/pig_sdk/__init__.py", regexp.MustCompile(`(?m)^MAX_FRAME_SIZE\s*=\s*(.+)$`)},
	}
	for _, c := range cases {
		got := readFrameConst(t, c.rel, c.re)
		if got != MaxFrameSize {
			t.Errorf("%s frame cap = %d, host MaxFrameSize = %d; constants must stay in sync", c.name, got, MaxFrameSize)
		}
	}
}

func TestLivenessWireSDKsInSync(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	files := map[string][]string{
		"go":     {"extensions/sdk/protocol.go", "extensions/sdk/extension.go", "extensions/sdk/context.go"},
		"rust":   {"extensions/sdk-rs/src/protocol.rs", "extensions/sdk-rs/src/extension.rs", "extensions/sdk-rs/src/context.rs"},
		"python": {"extensions/sdk-py/pig_sdk/__init__.py"},
		"node":   {"coding/extension/host/subprocess/runtime-node/runtime.mjs"},
	}
	for sdk, paths := range files {
		var source strings.Builder
		for _, rel := range paths {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err != nil {
				t.Fatalf("read %s SDK source %s: %v", sdk, rel, err)
			}
			source.Write(data)
		}
		for _, token := range []string{"ping", "pong", "request_state", "parent_request_id", "blocked", "progress", "completed"} {
			if !strings.Contains(source.String(), token) {
				t.Errorf("%s SDK omits current liveness wire token %q", sdk, token)
			}
		}
	}
}
