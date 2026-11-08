package hostenv

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Ports apps/p2/test/p2/terminal-daemon.test.ts.

type requestFunc func(ctx context.Context, op string, request env.Json, options env.RequestOptions) (env.Reply, error)

func (f requestFunc) Request(ctx context.Context, op string, request env.Json, options env.RequestOptions) (env.Reply, error) {
	return f(ctx, op, request, options)
}

func TestTerminalCapabilityProbeValidatesPTYSupportWithoutStartingAShell(t *testing.T) {
	err := RequireTerminalSupport(context.Background(), requestFunc(func(_ context.Context, op string, request env.Json, _ env.RequestOptions) (env.Reply, error) {
		if op != "pty" {
			t.Errorf("op = %q", op)
		}
		if !reflect.DeepEqual(request, env.Json{"cwd": "", "cols": 0, "rows": 0}) {
			t.Errorf("request = %v", request)
		}
		return env.Reply{}, env.NewRemoteError(env.Json{"code": "EINVAL", "message": "cols must be between 1 and 4096"})
	}))
	if err != nil {
		t.Fatal(err)
	}
}

func TestTerminalCapabilityProbeRejectsReleasedDaemonsThatPredatePTYSupport(t *testing.T) {
	// The released 1.1.0 daemon and the source build have the same version, but different operations.
	failure := env.NewRemoteError(env.Json{"code": "EINVAL", "message": "unknown operation pty"})
	err := RequireTerminalSupport(context.Background(), requestFunc(func(context.Context, string, env.Json, env.RequestOptions) (env.Reply, error) {
		return env.Reply{}, failure
	}))
	if err != error(failure) {
		t.Fatalf("err = %v, want the daemon's own error", err)
	}
}

func TestTerminalCapabilityProbeDoesNotMistakeOtherFailuresOrSuccessfulInvalidRequestsForSupport(t *testing.T) {
	failure := env.NewRemoteError(env.Json{"code": "spawn_error", "message": "daemon unavailable"})
	err := RequireTerminalSupport(context.Background(), requestFunc(func(context.Context, string, env.Json, env.RequestOptions) (env.Reply, error) {
		return env.Reply{}, failure
	}))
	if err != error(failure) {
		t.Fatalf("err = %v", err)
	}
	err = RequireTerminalSupport(context.Background(), requestFunc(func(context.Context, string, env.Json, env.RequestOptions) (env.Reply, error) {
		return env.Reply{JSON: env.Json{}, Session: 1}, nil
	}))
	if err == nil || !strings.Contains(err.Error(), "accepted an invalid terminal size") {
		t.Fatalf("err = %v", err)
	}
}

// pig additive: the probe is abandoned after 5 s, as Pi's timer rejects.
func TestTerminalCapabilityProbeTimesOut(t *testing.T) {
	var cancelled = make(chan struct{})
	start := time.Now()
	err := RequireTerminalSupport(context.Background(), requestFunc(func(ctx context.Context, _ string, _ env.Json, _ env.RequestOptions) (env.Reply, error) {
		<-ctx.Done()
		close(cancelled)
		return env.Reply{}, ctx.Err()
	}))
	if err == nil || err.Error() != "pi-env terminal capability probe timed out" {
		t.Fatalf("err = %v", err)
	}
	if elapsed := time.Since(start); elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf("elapsed %s", elapsed)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the abandoned request keeps running")
	}
}

func TestTerminalCapabilityProbeEndsWithItsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := RequireTerminalSupport(ctx, requestFunc(func(ctx context.Context, _ string, _ env.Json, _ env.RequestOptions) (env.Reply, error) {
		<-ctx.Done()
		return env.Reply{}, ctx.Err()
	}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestLocalSystemNamesTheSystemAsNodeDoes(t *testing.T) {
	platform, arch := LocalSystem()
	if platform == "" || arch == "" || arch == "amd64" {
		t.Fatalf("LocalSystem = %q, %q", platform, arch)
	}
	for goarch, want := range map[string]string{"amd64": "x64", "arm64": "arm64", "386": "ia32", "ppc64le": "ppc64", "mipsle": "mipsel", "riscv64": "riscv64"} {
		if got := nodeArch(goarch); got != want {
			t.Errorf("nodeArch(%s) = %s, want %s", goarch, got, want)
		}
	}
}

func TestTerminalDaemonRefusesWindows(t *testing.T) {
	_, err := TerminalDaemon(context.Background(), "windows", "x64", false)
	if err == nil || err.Error() != "Terminals are not supported on Windows yet" {
		t.Fatalf("err = %v", err)
	}
}

// A foreign system's packaged daemon cannot run here: it is returned as it is, and checked over its SSH connection.
func TestTerminalDaemonReturnsAForeignSystemsPackagedBinaryUnprobed(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("PI_ENV_DAEMON_DIR", directory)
	t.Setenv("P2_TERMINAL_DAEMON", "")
	platform, arch := foreign()
	binary := filepath.Join(directory, "pi-env-"+platform+"-"+arch, "pi-env")
	if _, err := TerminalDaemon(context.Background(), platform, arch, false); err == nil || !strings.Contains(err.Error(), "No pi-env daemon with terminal support for "+platform+"-"+arch) {
		t.Fatalf("missing: err = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("not a daemon"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := TerminalDaemon(context.Background(), platform, arch, false)
	if err != nil || got != binary {
		t.Fatalf("got %q, %v; want %q", got, err, binary)
	}
	// P2_TERMINAL_DAEMON is for this machine only.
	t.Setenv("P2_TERMINAL_DAEMON", "/nonexistent")
	if got, err := TerminalDaemon(context.Background(), platform, arch, true); err != nil || got != binary {
		t.Fatalf("local: got %q, %v", got, err)
	}
}

func foreign() (platform, arch string) {
	local, _ := LocalSystem()
	if local == "linux" {
		return "darwin", "arm64"
	}
	return "linux", "arm64"
}

// This machine's candidates are probed: one without the `pty` operation is reported with its failure.
func TestTerminalDaemonProbesThisMachinesCandidates(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("PI_ENV_DAEMON_DIR", directory)
	platform, arch := LocalSystem()
	if platform == "windows" {
		t.Skip("no terminals on Windows")
	}
	build := filepath.Join(directory, "built", "pi-env")
	command := exec.Command("go", "build", "-trimpath", "-o", build, "github.com/MichaelKinsy/PiG/env/cmd/pi-env")
	command.Dir = testenv.ModuleRoot(t)
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("building the daemon: %v\n%s", err, output)
	}
	t.Setenv("P2_TERMINAL_DAEMON", build)
	got, err := TerminalDaemon(context.Background(), platform, arch, true)
	if err != nil || got != build {
		t.Fatalf("got %q, %v", got, err)
	}
	// Not for a remote system's lookup: only the packaged layout counts.
	if _, err := TerminalDaemon(context.Background(), platform, arch, false); err == nil || !strings.Contains(err.Error(), platform+"-"+arch) {
		t.Fatalf("remote lookup: err = %v", err)
	}
	// A candidate that is not a daemon is listed with its failure.
	broken := filepath.Join(directory, "broken")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("P2_TERMINAL_DAEMON", broken)
	_, err = TerminalDaemon(context.Background(), platform, arch, true)
	if err == nil || !strings.HasPrefix(err.Error(), "No pi-env daemon with terminal support: run `make build-env-daemons`") || !strings.Contains(err.Error(), broken+": ") {
		t.Fatalf("err = %v", err)
	}
	// The packaged daemon of this machine is used when P2_TERMINAL_DAEMON names nothing.
	t.Setenv("P2_TERMINAL_DAEMON", "")
	packaged := filepath.Join(directory, "pi-env-"+platform+"-"+arch, "pi-env")
	if err := os.MkdirAll(filepath.Dir(packaged), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(build)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(packaged, data, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := TerminalDaemon(context.Background(), platform, arch, true); err != nil || got != packaged {
		t.Fatalf("packaged: got %q, %v", got, err)
	}
}
