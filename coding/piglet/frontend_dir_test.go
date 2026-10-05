package piglet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestFrontendDirResolvesInsideThePigletDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tern"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	testenv.RequireDirectoryLink(t, outside, filepath.Join(root, "escape"))
	if err := os.WriteFile(filepath.Join(root, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		frontend, want, err string
	}{
		{frontend: "", want: ""},
		{frontend: "tern", want: canonicalPath(filepath.Join(root, "tern"))},
		{frontend: "escape", err: "leaves the Piglet directory"},
		{frontend: "missing", err: "does not exist"},
		{frontend: "file", err: "is not a directory"},
	} {
		t.Run(tc.frontend, func(t *testing.T) {
			document := "name: app\n"
			if tc.frontend != "" {
				document += "build:\n  frontend: " + tc.frontend + "\n"
			}
			path := filepath.Join(root, "app.yaml")
			if err := os.WriteFile(path, []byte(document), 0o644); err != nil {
				t.Fatal(err)
			}
			p, err := Parse(path)
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.FrontendDir()
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("FrontendDir() = %q, %v; want error %q", got, err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("FrontendDir() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
