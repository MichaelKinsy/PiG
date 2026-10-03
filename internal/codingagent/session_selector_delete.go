// Ports packages/coding-agent/src/modes/interactive/components/session-selector.ts (deleteSessionFile).
package codingagent

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

const (
	sessionDeleteTrash  = "trash"
	sessionDeleteUnlink = "unlink"
)

// sessionDeleteResult is deleteSessionFile's { ok, method, error } result.
type sessionDeleteResult struct {
	ok     bool
	method string
	error  string
}

// trashSpawnResult holds the spawnSync result fields deleteSessionFile reads. status is meaningful only when the process exited; error is the spawn error message.
type trashSpawnResult struct {
	status int
	exited bool
	error  string
	stderr string
}

// deleteSessionFile tries the `trash` command first, then falls back to unlink. As with Pi's spawnSync, trash runs to completion on the calling goroutine without terminal stdio. Unix executable text uses execvp's shell fallback.
func deleteSessionFile(sessionPath string) sessionDeleteResult {
	trashArgs := []string{sessionPath}
	if strings.HasPrefix(sessionPath, "-") {
		trashArgs = []string{"--", sessionPath}
	}
	trashResult := spawnTrash(trashArgs)

	getTrashErrorHint := func() string {
		var parts []string
		if trashResult.error != "" {
			parts = append(parts, trashResult.error)
		}
		if stderr := jsTrim(trashResult.stderr); stderr != "" {
			first, _, _ := strings.Cut(stderr, "\n")
			parts = append(parts, first)
		}
		if len(parts) == 0 {
			return ""
		}
		return "trash: " + jsstring.Slice(strings.Join(parts, " · "), 0, 200)
	}

	// A successful trash, or a file that is gone afterwards, counts as moved to trash.
	if (trashResult.exited && trashResult.status == 0) || !sessionPathExists(sessionPath) {
		return sessionDeleteResult{ok: true, method: sessionDeleteTrash}
	}
	if err := unlinkSessionFile(sessionPath); err != nil {
		unlinkError := tools.NodeFSError(err, "unlink", sessionPath)
		if hint := getTrashErrorHint(); hint != "" {
			unlinkError += " (" + hint + ")"
		}
		return sessionDeleteResult{method: sessionDeleteUnlink, error: unlinkError}
	}
	return sessionDeleteResult{ok: true, method: sessionDeleteUnlink}
}

// spawnTrash runs `trash` as spawnSync("trash", args, { encoding: "utf-8" }) does, keeping only the fields deleteSessionFile reads.
func spawnTrash(args []string) trashSpawnResult {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := &trashOutput{cancel: cancel}
	cmd, runErr := runTrash(ctx, args, output)
	result := trashSpawnResult{stderr: jsstring.FromUTF8(output.stderr.Bytes())}
	if output.overflow {
		result.error = "spawnSync trash ENOBUFS"
	} else if cmd == nil || cmd.ProcessState == nil {
		result.error = "spawnSync trash " + trashSpawnErrorCode(runErr)
	}
	if cmd != nil && cmd.ProcessState != nil {
		result.exited = cmd.ProcessState.Exited()
		result.status = cmd.ProcessState.ExitCode()
	}
	return result
}

func runTrashCommand(ctx context.Context, path string, args []string, output *trashOutput) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Args[0] = "trash"
	linkerexec.Prepare(cmd)
	cmd.Cancel = func() error { return terminateTrash(cmd.Process) }
	cmd.Stdout = trashOutputWriter{output: output}
	cmd.Stderr = trashOutputWriter{output: output, stderr: true}
	err := cmd.Run()
	return cmd, err
}

// Node child_process.spawnSync defaults maxBuffer to 1 MiB across stdout and stderr and terminates the child with SIGTERM on overflow. Only stderr reaches the deletion hint. Both pipe readers and the child settle before spawnTrash returns.
const trashMaxBuffer = 1024 * 1024

type trashOutput struct {
	mu       sync.Mutex
	stderr   bytes.Buffer
	total    int
	overflow bool
	cancel   context.CancelFunc
}

type trashOutputWriter struct {
	output *trashOutput
	stderr bool
}

func (w trashOutputWriter) Write(p []byte) (int, error) {
	out := w.output
	out.mu.Lock()
	defer out.mu.Unlock()
	if w.stderr {
		out.stderr.Write(p[:min(len(p), trashMaxBuffer-out.stderr.Len())])
	}
	if !out.overflow {
		out.total += len(p)
		if out.total > trashMaxBuffer {
			out.overflow = true
			out.cancel()
		}
	}
	return len(p), nil
}

// trashSpawnErrorCode names a start failure by its Node error code, as spawnSync's error message does.
func trashSpawnErrorCode(err error) string {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, exec.ErrNotFound) {
		return "ENOENT"
	}
	if code := trashSystemErrorCode(err); code != "" {
		return code
	}
	return err.Error()
}

// sessionPathExists is Node's existsSync, which follows symbolic links and junctions.
func sessionPathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
