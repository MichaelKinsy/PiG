package tools

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// toolProbeTimeout bounds one "<bin> --version" probe so a program that never
// exits cannot stall tool lookup.
const toolProbeTimeout = 5 * time.Second

// toolProbeKey identifies one probed file revision: a replaced or edited
// binary probes again, an unchanged one never does (autocomplete resolves the
// tool per keystroke).
type toolProbeKey struct {
	path    string
	tool    string
	modTime time.Time
	size    int64
}

var toolProbeCache sync.Map // toolProbeKey -> bool

// systemToolIdentity reports whether the program at path is the tool the
// caller asked for. Upstream's commandExists accepts any program that spawns
// without ENOENT, so Debian's FDclone (/usr/bin/fd, a file manager that opens
// /dev/tty) passed as fd. PiG instead runs "<path> --version" and accepts only
// exit 0 with stdout that starts with one of the tool's version prefixes
// (unlike Pi). The probe is detached from the terminal (see
// nodespawn.DetachFromTerminal) and reads /dev/null.
func systemToolIdentity(path, tool string, prefixes []string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	key := toolProbeKey{path: path, tool: tool, modTime: info.ModTime(), size: info.Size()}
	if v, ok := toolProbeCache.Load(key); ok {
		return v.(bool)
	}
	ok := probeToolVersion(path, prefixes)
	toolProbeCache.Store(key, ok)
	return ok
}

func probeToolVersion(path string, prefixes []string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), toolProbeTimeout)
	defer cancel()
	//nolint:gosec // G204 (class b): program is a candidate found on PATH for fd or rg; the only argument is the fixed --version.
	cmd := exec.CommandContext(ctx, path, "--version")
	// stdin stays nil: os/exec opens the null device. Stdout is a pipe, so the
	// probe has no terminal on any standard stream.
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.WaitDelay = time.Second
	nodespawn.DetachFromTerminal(cmd)
	if err := cmd.Run(); err != nil {
		return false
	}
	out := stdout.String()
	for _, p := range prefixes {
		if strings.HasPrefix(out, p) {
			return true
		}
	}
	return false
}

// systemToolPath finds the first name on PATH whose binary passes the identity
// probe. Windows returns the bare name, as upstream's getToolPath does.
func systemToolPath(tool string, config systemToolConfig) string {
	for _, name := range systemBinaryNames(config) {
		path, err := nodespawn.LookPath(name, "", nil)
		if err != nil || !systemToolIdentity(path, tool, config.VersionPrefixes) {
			continue
		}
		if runtime.GOOS == "windows" {
			return name
		}
		return path
	}
	return ""
}
