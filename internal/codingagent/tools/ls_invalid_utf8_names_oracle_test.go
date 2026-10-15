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

// Pi's ls (core/tools/ls.ts) reads names with fs.readdir, which decodes them as UTF-8 and writes U+FFFD for each ill-formed subpart; the stat on the decoded name then fails with ENOENT and the entry is skipped like a broken link. Pig must list the same names, not the raw bytes.
func TestLsSkipsNamesThatAreNotValidUTF8LikePi(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("file names are arbitrary bytes only on Linux")
	}
	dir := t.TempDir()
	for _, name := range []string{"ok.txt", "Ünï.txt", "a\xffb.txt", "\xe2\x82", "z\xed\xa0\x80", "\xc0\xaf.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Skipf("file system refuses the name %q: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "d\xff"), 0o700); err != nil {
		t.Skipf("file system refuses the directory name: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.CommandContext(t.Context(), "node", "testdata/real_fs_tool.mjs", pigversion.UpstreamPackageVersion, "ls", dir, "{}")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want struct{ Text string }
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	result, err := (&LsTool{CWD: dir}).Execute(context.Background(), "call", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := result.Content[0].(ai.TextContent).Text
	if got != want.Text {
		t.Errorf("Pig lists %q, Pi lists %q", got, want.Text)
	}
}
