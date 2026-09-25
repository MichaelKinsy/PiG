//go:build parity

package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTokenContext_ExpandsTempPlaceholder(t *testing.T) {
	tc := newTokenContext(t, "test")

	got := tc.expand("{{TEMP}}/sessions")
	want := filepath.Join(tc.tempRoot, "sessions")
	if got != want {
		t.Errorf("expand: got %q, want %q", got, want)
	}

	// No-op when no token present.
	if tc.expand("/tmp/literal") != "/tmp/literal" {
		t.Errorf("expand should return input unchanged when no token")
	}
}

func TestTokenContext_PerCallFreshTempdir(t *testing.T) {
	a := newTokenContext(t, "a")
	b := newTokenContext(t, "b")
	if a.tempRoot == b.tempRoot {
		t.Fatalf("two contexts must allocate distinct tempdirs; both got %q", a.tempRoot)
	}
	// Both must exist on disk.
	for _, p := range []string{a.tempRoot, b.tempRoot} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("tempdir %q does not exist: %v", p, err)
		}
	}
}

func TestTokenContext_ExpandSliceIsIndependent(t *testing.T) {
	tc := newTokenContext(t, "slice")
	in := []string{"--session-dir", "{{TEMP}}/s", "--log", "{{TEMP}}/l"}
	out := tc.expandSlice(in)
	if &in[0] == &out[0] {
		t.Fatalf("expandSlice must return a new slice, not modify in-place")
	}
	if !strings.Contains(out[1], tc.tempRoot) || !strings.Contains(out[3], tc.tempRoot) {
		t.Errorf("expandSlice did not expand all entries: %v", out)
	}
	if in[1] != "{{TEMP}}/s" {
		t.Errorf("expandSlice mutated input: %v", in)
	}
}

func TestContainsLiteralTmpPath_DetectsBypass(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want bool
	}{
		{"clean tokens", []string{"--session-dir", "{{TEMP}}/s"}, false},
		{"literal /tmp", []string{"--session-dir", "/tmp/x"}, true},
		{"embedded =/tmp/", []string{"--log=/tmp/y.log"}, true},
		{"deep /tmp path", []string{"/tmp/nested/path"}, true},
		{"relative is fine", []string{"sessions"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, got := containsLiteralTmpPath(c.in)
			if got != c.want {
				t.Errorf("want %v, got %v for %v", c.want, got, c.in)
			}
		})
	}
}
