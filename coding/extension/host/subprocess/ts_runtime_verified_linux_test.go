//go:build linux

package subprocess

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/tsruntime"
)

// envSelfDigest makes a copy of this test binary print what it is, instead of running tests, so a test can see which file a start ran.
const envSelfDigest = "PIG_SUBPROCESS_TEST_SELF_DIGEST"

func init() {
	if os.Getenv(envSelfDigest) != "1" {
		return
	}
	data, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	sum := sha256.Sum256(data)
	fmt.Println(hex.EncodeToString(sum[:]), os.Args[0], strings.Join(os.Args[1:], " "))
	os.Exit(0)
}

func writeSelfCopy(t *testing.T, path, marker string) string {
	t.Helper()
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, marker...)
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// The process a launch starts runs the bytes that were verified, whatever is at the executable's path by then: the command runs the
// verified descriptor and keeps the path as argv[0].
func TestTypeScriptRuntimeLaunchRunsTheVerifiedFileAfterTheSwap(t *testing.T) {
	t.Setenv(envSelfDigest, "1")
	root := t.TempDir()
	dir := filepath.Join(root, "aot")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "pig-ts-host")
	verified := writeSelfCopy(t, exe, "A")
	manifest := tsruntime.Manifest{ID: "aot", Kind: tsruntime.KindExecutable, Version: "1", Platform: tsruntime.Platform(), Provider: "test", Image: &tsruntime.Image{Executable: "pig-ts-host", SHA256: verified}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, tsruntime.ManifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
	sel := tsruntime.NewResolver(root, tsruntime.NewVerifier(nil)).Resolve(t.Context(), tsruntime.Request{ID: "aot", Source: tsruntime.SourceEnv}, tsruntime.Set{})
	if sel.Runtime != "aot" || sel.Verified == nil {
		t.Fatalf("selection %+v (fallback %+v), want the verified slot executable", sel, sel.Fallback)
	}
	defer releaseVerified(sel)

	replacement := filepath.Join(dir, "replacement")
	swapped := writeSelfCopy(t, replacement, "B")
	if err := os.Rename(replacement, exe); err != nil {
		t.Fatal(err)
	}
	cmd := typeScriptRuntimeCommand(t.Context(), sel, filepath.Join(root, "tree"), "cli.mjs", "node extension loader", []string{"/a.ts"})
	if !strings.HasPrefix(cmd.Path, "/proc/self/fd/") || cmd.Args[0] != exe {
		t.Fatalf("command runs %q with argv[0] %q, want the verified descriptor with argv[0] %s", cmd.Path, cmd.Args[0], exe)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 4 || fields[0] != verified || fields[1] != exe || fields[2] != "cli" || fields[3] != "/a.ts" {
		t.Fatalf("the process printed %q; want digest %s (not the swapped %s), argv[0] %s and the arguments cli /a.ts", out, verified, swapped, exe)
	}
	// The launch released the descriptor once the process started; a second command from the same selection is refused rather than
	// naming whatever file now holds that descriptor number.
	releaseVerified(sel)
	if again := typeScriptRuntimeCommand(t.Context(), sel, filepath.Join(root, "tree"), "cli.mjs", "node extension loader", nil); again.Err == nil {
		t.Fatalf("a command from a released selection runs %q", again.Path)
	}
}
