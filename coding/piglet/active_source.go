package piglet

import (
	"os"
	"path/filepath"
	"strings"
)

// ActiveSourceEnv is the variable a running session exports to the processes its tools start, so `pig status`, `pig diagnose`
// and `pig piglet show` run from the agent's shell can see the Piglet the session runs. The value is the Piglet source path,
// or the Piglet's name for a Piglet Binary, whose Piglet has no source file. The variable is informational: no run selects
// a Piglet from it.
//
// pig additive (D209): the active Piglet is exported to tool subprocesses as PIG_ACTIVE_PIGLET_SOURCE.
const ActiveSourceEnv = "PIG_ACTIVE_PIGLET_SOURCE"

// ActiveSourceValue returns the value of [ActiveSourceEnv] for p: its name when it is a Piglet Binary's baked Piglet (baked), else
// its source path, else its name. A nil p has no value.
func ActiveSourceValue(p *Piglet, baked bool) string {
	if p == nil {
		return ""
	}
	if !baked && p.SourcePath() != "" {
		return p.SourcePath()
	}
	return p.Name
}

// ActiveSourceIsPath reports whether an [ActiveSourceEnv] value names a Piglet file, as opposed to the name of a Piglet Binary.
func ActiveSourceIsPath(value string) bool {
	lower := strings.ToLower(value)
	return strings.ContainsRune(value, filepath.Separator) || strings.ContainsRune(value, '/') || strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}

// ActiveSourceFromEnv returns the [ActiveSourceEnv] value of this process, or "".
func ActiveSourceFromEnv() string {
	//perf:ok read once per status, diagnose or show command
	return strings.TrimSpace(os.Getenv(ActiveSourceEnv))
}

// runningBinary reports the Piglet Binary this process is. The CLI sets it with [SetRunningBinary]; stock pig sets none.
var runningBinary func() (name string, closed, ok bool)

// SetRunningBinary lets `pig piglet show` name whether the Piglet Binary it runs in is closed (build.closed). The package that owns
// the baked Piglet cannot be imported from here, so the CLI hands the answer in.
//
// pig additive (D202): `pig piglet show` on a Piglet Binary prints whether it is closed.
func SetRunningBinary(fn func() (name string, closed, ok bool)) { runningBinary = fn }

// runningBinaryNamed reports the open or closed state of the running Piglet Binary when its Piglet is called name.
func runningBinaryNamed(name string) (closed, ok bool) {
	if runningBinary == nil {
		return false, false
	}
	binaryName, closed, ok := runningBinary()
	if !ok || binaryName != name {
		return false, false
	}
	return closed, true
}
