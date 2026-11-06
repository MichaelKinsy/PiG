package tools

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// toolProbeTimeout bounds one "<bin> --version" probe so a program that never
// exits cannot stall tool lookup. Pi's commandExists has no bound.
// pig divergence (D269): the probe's 5 s bound and 1 s pipe grace are PiG's own.
const toolProbeTimeout = 5 * time.Second

// toolProbeKey identifies one probed file revision: a replaced or edited
// binary probes again, an unchanged one never does. LookupToolPath runs when
// the autocomplete provider is built, at startup and in every find and grep
// call, so most calls hit this cache.
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
// (unlike Pi, see D269). The probe is detached from the terminal (see
// nodespawn.DetachFromTerminal) and reads /dev/null. Only a probe that ran to
// completion is cached: a timeout or a failed start (a package manager still
// writing the file) is tried again at the next lookup.
func systemToolIdentity(path, tool string, prefixes []string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	key := toolProbeKey{path: path, tool: tool, modTime: info.ModTime(), size: info.Size()}
	if v, ok := toolProbeCache.Load(key); ok {
		return v.(bool)
	}
	ok, completed := probeToolVersion(path, prefixes)
	if completed {
		toolProbeCache.Store(key, ok)
	}
	return ok
}

// probeToolVersion runs "<path> --version" and reports whether stdout starts
// with one of prefixes after an exit 0. completed is false when the probe was
// cut short or could not start, so its verdict says nothing about the binary.
func probeToolVersion(path string, prefixes []string) (ok, completed bool) {
	ctx, cancel := context.WithTimeout(context.Background(), toolProbeTimeout)
	defer cancel()
	//nolint:gosec // G204 (class b): program is a candidate found on PATH for fd or rg; the only argument is the fixed --version.
	cmd := exec.CommandContext(ctx, path, "--version")
	// stdin stays nil: os/exec opens the null device. Stdout is a pipe, so the
	// probe has no terminal on any standard stream.
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// pig divergence (D269): the probe's 5 s bound and 1 s pipe grace are PiG's own.
	cmd.WaitDelay = time.Second
	nodespawn.DetachFromTerminal(cmd)
	// Start the program as the searches do: execvp's /bin/sh fallback for a
	// file with no #! line, and Termux's linker launch.
	nodespawn.SetProgram(cmd)
	if err := cmd.Run(); err != nil {
		if _, exited := errors.AsType[*exec.ExitError](err); exited && ctx.Err() == nil {
			return false, true
		}
		return false, false
	}
	out := stdout.String()
	for _, p := range prefixes {
		if strings.HasPrefix(out, p) {
			return true, true
		}
	}
	return false, true
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
