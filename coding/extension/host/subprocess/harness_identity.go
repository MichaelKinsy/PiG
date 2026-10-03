package subprocess

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

// An extension that runs inside Pi runs in Pi's own Node process, so
// process.argv[1] is Pi's CLI entry and the arguments after it are the ones Pi
// was started with. Extensions re-launch the harness from that entry
// (`process.execPath process.argv[1] ...`: @henryqw/pi-subagent, pi-subagents).
// A PiG Node extension runs in its own process, whose runtime
// (runtime-node/process-identity.mjs) rebuilds that argv: argv[1] is
// runtime-node/harness/cli.mjs, which runs the binary named by
// harnessBinaryEnv, and the arguments are read from the file named by
// harnessArgvFileEnv. The arguments travel in a file rather than the
// environment so a long prompt argument cannot exceed the per-variable limit
// of exec, and so they do not leak into every process an extension starts.
const (
	harnessBinaryEnv   = "PIG_HARNESS_BINARY"
	harnessArgvFileEnv = "PIG_HARNESS_ARGV_FILE"
)

// harnessExecutable is the running binary, the harness an extension re-launch
// starts. An empty result leaves the harness entry to report the missing
// binary when an extension runs it.
func harnessExecutable() string {
	exe, err := linkerexec.Executable()
	if err != nil {
		return ""
	}
	return exe
}

// harnessEnv returns the environment entries that give an extension process
// this Host's harness identity. The argument file is written once per Host in
// its private runtime directory.
func (h *Host) harnessEnv() ([]string, error) {
	h.harnessArgvOnce.Do(func() {
		dir, err := h.ensureSockRuntimeDir()
		if err != nil {
			h.harnessArgvErr = err
			return
		}
		args := h.harnessArgs
		if args == nil {
			args = []string{}
		}
		data, err := json.Marshal(args)
		if err != nil {
			h.harnessArgvErr = err
			return
		}
		path := filepath.Join(dir, "harness-argv.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			h.harnessArgvErr = err
			return
		}
		h.harnessArgvPath = path
	})
	if h.harnessArgvErr != nil {
		return nil, h.harnessArgvErr
	}
	return []string{harnessBinaryEnv + "=" + h.harnessBinary, harnessArgvFileEnv + "=" + h.harnessArgvPath}, nil
}
