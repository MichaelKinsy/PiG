package subprocess

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A Node extension that dies reports why on stderr, which the host reads
// through a pipe. Node writes pipes asynchronously on POSIX, so a launcher that
// calls process.exit right after the write loses what the pipe has no room for.
// The reader here starts late, so the report is far larger than the pipe buffer
// when the launcher exits.
func TestNodeLauncherCrashReportSurvivesPipe(t *testing.T) {
	nodeCellRequireNode(t)
	entry, manifest := nodeCellFailureManifest(t, []string{"broken"})
	generationsPath, err := filepath.Abs("runtime-node/generations.mjs")
	if err != nil {
		t.Fatal(err)
	}
	const size = 512 << 10
	for _, launcher := range []struct{ file, arg string }{
		{"cli.mjs", entry},
		{"cell.mjs", manifest},
	} {
		t.Run(launcher.file, func(t *testing.T) {
			launcherPath, err := filepath.Abs(filepath.Join("runtime-node", launcher.file))
			if err != nil {
				t.Fatal(err)
			}
			script := fmt.Sprintf(`
import { pathToFileURL } from "node:url";
const { Generations } = await import(pathToFileURL(%q));
Generations.prototype.admit = async function () { return true; };
Generations.prototype.serve = async function () { throw new Error("serve boom " + "x".repeat(%d) + " end-marker"); };
process.argv[2] = %q;
await import(pathToFileURL(%q));
setTimeout(() => {}, 5000);
`, generationsPath, size, launcher.arg, launcherPath)
			cmd := exec.CommandContext(testbudget.Context(t), "node", "--input-type=module", "--eval", script)
			cmd.Stdin = strings.NewReader("")
			stderr, err := cmd.StderrPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			// Let the child fill the pipe and reach its exit before anything is read.
			time.Sleep(500 * time.Millisecond)
			out, readErr := io.ReadAll(stderr)
			waitErr := cmd.Wait()
			var exitErr *exec.ExitError
			if readErr != nil || !errors.As(waitErr, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("read %v, wait %v, want exit 1", readErr, waitErr)
			}
			if len(out) <= size || !strings.Contains(string(out), "end-marker") {
				t.Fatalf("stderr carried %d bytes, want the whole report (over %d) through end-marker", len(out), size)
			}
		})
	}
}
