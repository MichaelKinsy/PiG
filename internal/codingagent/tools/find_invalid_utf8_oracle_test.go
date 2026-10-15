package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// fd prints a path as its bytes and Pi's find (core/tools/find.ts) decodes each line as UTF-8, so a name that is not valid UTF-8 appears with U+FFFD for each ill-formed subpart. Pig must print the same rows. Single-file directories keep fd's order.
func TestFindOnNamesThatAreNotValidUTF8MatchesPi(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("file names are arbitrary bytes only on Linux")
	}
	if LookupToolPath("fd", "") == "" {
		t.Skip("neither fd nor fdfind is installed")
	}
	for _, name := range []string{"a\xffb.txt", "\xe2\x82", "z\xed\xa0\x80.md", "\xc0\xaf"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Skipf("file system refuses the name %q: %v", name, err)
		}
		for _, args := range []string{`{"pattern":"*"}`, `{"pattern":"**/*"}`} {
			cmd := exec.CommandContext(t.Context(), "node", "testdata/real_fs_tool.mjs", pigversion.UpstreamPackageVersion, "find", dir, args)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			output, err := cmd.Output()
			if err != nil {
				t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
			}
			var want struct{ Text, Thrown string }
			if err := json.Unmarshal(output, &want); err != nil {
				t.Fatal(err)
			}
			result, err := (&FindTool{CWD: dir}).Execute(context.Background(), "call", json.RawMessage(args), nil)
			got := ""
			switch {
			case err != nil:
				got, want.Text = err.Error(), want.Thrown
			case len(result.Content) > 0:
				got = result.Content[0].(ai.TextContent).Text
			}
			if got != want.Text {
				t.Errorf("%q %s\n  Pig %q\n  Pi  %q", name, args, got, want.Text)
			}
		}
	}
}
