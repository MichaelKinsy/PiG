// Package hostenv is the terminals plugin's view of the machines it runs terminals on: which pi-env daemon serves a
// system, whether it can open terminals, and the connections to this machine and to the SSH and sandbox environments of
// envs.json. It ports apps/p2/plugins/terminals/daemon.ts and envs.ts and the terminals host's connection logic; the
// connections themselves (SSH options, sandbox launcher, connection revision, secrets) are package envs.
//
// pig additive: Pi finds its daemon among Rust build outputs of packages/env; PiG's pi-env is the Go daemon of ./env/cmd/pi-env,
// a separate binary that the env package starts as a child (there is no in-process or embedded daemon behind env.Connection).
// It is found in the packaged layout `<dir>/pi-env-<platform>-<arch>/pi-env` (`make build-env-daemons`; env.PackagedDaemon,
// PI_ENV_DAEMON_DIR), or for this machine at P2_TERMINAL_DAEMON.
package hostenv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/env"
)

// probeTimeout is how long the terminal capability probe may take.
const probeTimeout = 5 * time.Second

// LocalSystem is this machine, as pi-env names systems (Node's platform and arch spellings, `windows` for win32).
func LocalSystem() (platform, arch string) {
	return runtime.GOOS, nodeArch(runtime.GOARCH)
}

// nodeArch is process.arch for a GOARCH.
func nodeArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	case "mipsle":
		return "mipsel"
	case "ppc64le":
		return "ppc64"
	}
	return goarch
}

// TerminalDaemon is the daemon binary for a system; local is this machine, which may also use P2_TERMINAL_DAEMON.
func TerminalDaemon(ctx context.Context, platform, arch string, local bool) (string, error) {
	system := platform + "-" + arch
	if platform == "windows" {
		return "", errors.New("Terminals are not supported on Windows yet")
	}
	localPlatform, localArch := LocalSystem()
	native := platform == localPlatform && arch == localArch
	var candidates []string
	if local {
		candidates = append(candidates, os.Getenv("P2_TERMINAL_DAEMON"))
	}
	candidates = append(candidates, env.PackagedDaemon(env.RemotePlatform{Platform: platform, Arch: arch}))
	var failures []string
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		// Foreign binaries cannot run here; their SSH connection is checked before use instead.
		if !native {
			return candidate, nil
		}
		probe := env.NewConnection(env.ConnectionOptions{Command: []string{candidate}})
		err := RequireTerminalSupport(ctx, probe)
		probe.Close()
		if err == nil {
			return candidate, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		failures = append(failures, candidate+": "+err.Error())
	}
	if local {
		detail := ""
		if len(failures) > 0 {
			detail = " (" + strings.Join(failures, "; ") + ")"
		}
		return "", fmt.Errorf("No pi-env daemon with terminal support: run `make build-env-daemons` (PI_ENV_DAEMON_DIR names its output), or set P2_TERMINAL_DAEMON%s", detail)
	}
	return "", fmt.Errorf("No pi-env daemon with terminal support for %s: build it with `make build-env-daemons`", system)
}

// Requester is what the capability probe needs of a connection: *env.Connection.
type Requester interface {
	Request(ctx context.Context, op string, request env.Json, options env.RequestOptions) (env.Reply, error)
}

// RequireTerminalSupport probes the operation, not the version: released 1.1.0 binaries predate PTY support. No process
// is spawned.
func RequireTerminalSupport(ctx context.Context, connection Requester) error {
	requestContext, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := connection.Request(requestContext, "pty", env.Json{"cwd": "", "cols": 0, "rows": 0}, env.RequestOptions{})
		if err == nil {
			done <- errors.New("pi-env accepted an invalid terminal size")
			return
		}
		if remote, ok := errors.AsType[*env.RemoteError](err); ok && remote.Code == "EINVAL" && strings.HasPrefix(remote.Message, "cols must be between ") {
			done <- nil
			return
		}
		done <- err
	}()
	timer := time.NewTimer(probeTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return errors.New("pi-env terminal capability probe timed out")
	case <-ctx.Done():
		return ctx.Err()
	}
}
