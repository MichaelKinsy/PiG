package subprocess

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// extLogEnvName is the environment variable that names the log an extension process's SDK writes its own diagnostics to: the stack of a handler panic it
// recovered, a host call it could not make, a frame it dropped. Pi reports a throwing handler as one line through its error path (print-mode.ts, showExtensionError
// in interactive-mode.ts) and never writes the stack to the console, so an SDK's diagnostics must not reach the terminal that the host forwards an extension's own
// stdout and stderr to (extensionOutput). The value is the path of the process's stderr log, which the host's load and crash diagnostics read, so the SDK's lines
// are there with everything the process wrote to its stderr. An SDK appends one whole diagnostic per write and writes to its stderr when the variable is unset or
// the log cannot be written, as it does when no host started it. What the Go runtime writes for a fatal error is not an SDK diagnostic and stays on stderr.
const extLogEnvName = "PIG_EXT_LOG"

// createExtLog creates a log that a process's SDK appends to by path while the host writes the process's stderr to the same file. Every write of the host's handle
// therefore goes to the end, as the SDK's do: a handle with its own offset would overwrite what the SDK wrote.
func createExtLog(pattern string) (*os.File, error) {
	created, err := os.CreateTemp("", pattern)
	if err != nil {
		return nil, err
	}
	path := created.Name()
	_ = created.Close()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return file, nil
}

// extLogCreateEnvName, set to "1" beside PIG_EXT_LOG, lets the Go SDK create the log on its first write (extensions/sdk diagnostic.go). Only the fused extensions get it.
const extLogCreateEnvName = "PIG_EXT_LOG_CREATE"

// withExtLog is env with PIG_EXT_LOG naming path, in place of any entry the host's own environment carries (an extension that starts a pig passes its own). An empty path
// leaves no entry, so the process's SDK writes its diagnostics to stderr. The log of a process of its own is made by the host, so no entry lets the SDK create it: a pig that
// a fused extension started would otherwise pass the permission of its parent's log on.
func withExtLog(env []string, path string) []string {
	env = slices.DeleteFunc(slices.Clone(env), func(value string) bool {
		return envHasKey(value, extLogEnvName) || envHasKey(value, extLogCreateEnvName)
	})
	if path == "" {
		return env
	}
	return append(env, extLogEnvName+"="+path)
}

// fusedExtLog is the log of the fused extensions, which run in the host's own process and share its environment, as they share PIG_EXT_HOST_INFO. There is no
// stderr copy of theirs to read it with: a fused extension writes its stderr to the host's own. Nothing reads the file but a person who looks for it, so the Host
// names a path and does not create the file: the SDK creates it when it writes the first diagnostic, and a session in which no handler fails puts nothing in the
// temporary directory, as Pi's does. The Host removes it when it closes and when the process ends through TerminateHostProcesses; a process that is killed
// outright after a diagnostic leaves it.
type fusedExtLog struct {
	mu   sync.Mutex
	path string
}

// export chooses the log's path on first use and points this process's PIG_EXT_LOG at it, with the permission for the SDK to create it. The name is 128 random bits,
// so it cannot be guessed to plant a link at it, and the SDK's creating open does not follow one where the platform has the flag.
func (l *fusedExtLog) export() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.path == "" {
		l.path = filepath.Join(os.TempDir(), "pig-ext-fused-"+rand.Text()+".log")
	}
	_ = os.Setenv(extLogEnvName, l.path)
	_ = os.Setenv(extLogCreateEnvName, "1")
}

// remove deletes the log if a diagnostic created it, and the entries if a later Host in this process has not replaced them.
func (l *fusedExtLog) remove() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.path == "" {
		return
	}
	//perf:ok once per fused Host close, not per event: it must read the live variable because a later Host in this process may have replaced the entry
	if os.Getenv(extLogEnvName) == l.path {
		_ = os.Unsetenv(extLogEnvName)
		_ = os.Unsetenv(extLogCreateEnvName)
	}
	_ = os.Remove(l.path)
	l.path = ""
}
