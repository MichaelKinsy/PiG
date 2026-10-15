package env

// Ports packages/env/test/remote.test.ts (nodePlatform, listFiles through an old daemon, cancelling a listing),
// packages/env/test/old-daemon.ts and packages/env/test/ssh.test.ts (lists files over SSH).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/env/daemon"
)

// runOldDaemon is old-daemon.ts: a stand-in for a daemon released before `hello.features`. It answers `hello` without
// `features` and refuses every other operation as an unknown one, as such a daemon refuses `listFiles`.
func runOldDaemon(input io.Reader, output io.Writer, token string) int {
	if _, err := fmt.Fprintf(output, "PI-ENV %s\n", token); err != nil {
		return 1
	}
	for {
		request, err := daemon.ReadFrame(input)
		if err != nil || request == nil {
			return 0
		}
		if request.Kind != daemon.FrameRequest {
			continue
		}
		object, _ := request.JSON.(daemon.Object)
		reply := &daemon.Frame{Kind: daemon.FrameError, ID: request.ID, JSON: daemon.Object{"code": "EINVAL", "message": fmt.Sprintf("unknown operation %v", object["op"])}}
		if object["op"] == "hello" {
			reply = &daemon.Frame{Kind: daemon.FrameResult, ID: request.ID, JSON: daemon.Object{
				"protocol": 1, "version": "1.1.0", "os": "linux", "arch": "x86_64", "home": "/", "separator": "/", "tmpdir": "/tmp",
				"cwd": "/", "driveCwds": daemon.Object{}, "pid": os.Getpid(),
			}}
		}
		if err := daemon.WriteFrame(output, reply); err != nil {
			return 1
		}
	}
}

// remote.test.ts:73 "reports the remote system in Node's names": the daemon's `os` and `arch` are Rust's spelling.
func TestNodePlatformNamesTheRemoteSystemAsNodeDoes(t *testing.T) {
	t.Parallel()
	_, connection := remoteEnvironment(t)
	info, err := connection.Info(background)
	if err != nil {
		t.Fatal(err)
	}
	arch := map[string]string{"amd64": "x64", "arm64": "arm64", "386": "ia32"}[runtime.GOARCH]
	if arch == "" {
		arch = runtime.GOARCH
	}
	got := NodePlatform(info)
	if got.Platform != runtime.GOOS {
		t.Errorf("NodePlatform(%q, %q).Platform = %q, want %q", info.OS, info.Arch, got.Platform, runtime.GOOS)
	}
	// CI runs the darwin-x64 release daemon on arm64 macOS through Rosetta, so it reports x64 to an arm64 host.
	rosetta := runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" && got.Arch == "x64"
	if !rosetta && got.Arch != arch {
		t.Errorf("NodePlatform(%q, %q).Arch = %q, want %q", info.OS, info.Arch, got.Arch, arch)
	}
	for _, test := range []struct{ os, arch, platform, nodeArch string }{
		{"windows", "aarch64", "win32", "arm64"},
		{"macos", "x86_64", "darwin", "x64"},
		{"linux", "x86", "linux", "ia32"},
		{"android", "riscv64", "android", "riscv64"},
	} {
		if got := NodePlatform(RemoteInfo{OS: test.os, Arch: test.arch}); got != (NodeSystem{Platform: test.platform, Arch: test.nodeArch}) {
			t.Errorf("NodePlatform(%q, %q) = %+v, want %s/%s", test.os, test.arch, got, test.platform, test.nodeArch)
		}
	}
}

// remote.test.ts:81 "reports listFiles as not supported by daemons released before it".
func TestListFilesIsNotSupportedByDaemonsReleasedBeforeIt(t *testing.T) {
	t.Parallel()
	environment, _ := remoteEnvironment(t)
	info, err := environment.Connection.Info(background)
	if err != nil || !slices.Contains(info.Features, "listFiles") {
		t.Fatalf("hello features = %v, %v; want listFiles", info.Features, err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	connection := NewConnection(ConnectionOptions{Command: []string{self, "old-daemon"}})
	t.Cleanup(connection.Close)
	old := NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: connection, ID: "pi-env:old", Cwd: environment.Cwd()})
	_, err = old.ListFiles(background, ".", nil)
	if got := errorCodeOf(err); got != "not_supported" {
		t.Fatalf("ListFiles through a daemon without features: %v (code %s), want not_supported", err, got)
	}
	oldInfo, _ := connection.Info(background)
	if oldInfo.Features != nil {
		t.Errorf("an old daemon's features = %v, want none", oldInfo.Features)
	}
}

// remote.test.ts:92 "cancels a listing in the daemon": the cancel follows the request at once, bypassing the client's own
// abort check, so the daemon stops the walk.
func TestListFilesCancelsAListingInTheDaemon(t *testing.T) {
	t.Parallel()
	environment, connection := remoteEnvironment(t)
	for directory := range 100 {
		mustDo(os.MkdirAll(filepath.Join(environment.Cwd(), fmt.Sprintf("d%d", directory)), 0o755))
		for file := range 100 {
			mustDo(os.WriteFile(filepath.Join(environment.Cwd(), fmt.Sprintf("d%d", directory), fmt.Sprintf("f%d", file)), nil, 0o644))
		}
	}
	cancelled, cancel := context.WithCancel(background)
	cancel()
	_, err := connection.Request(cancelled, "listFiles", Json{"path": environment.Cwd()}, RequestOptions{})
	if remote, ok := errors.AsType[*RemoteError](err); !ok || remote.Code != "aborted" {
		t.Fatalf("a listing cancelled with its request = %v, want a remote `aborted`", err)
	}
	listed := must(environment.ListFiles(background, ".", nil))
	if len(listed.Entries) != 10_100 || listed.Truncated {
		t.Fatalf("after the cancel: %d entries, truncated %v; want 10100, false", len(listed.Entries), listed.Truncated)
	}
	bounded := must(environment.ListFiles(background, ".", &durableenv.ListFilesOptions{MaxEntries: new(5000.0)}))
	if len(bounded.Entries) != 5000 || !bounded.Truncated {
		t.Fatalf("MaxEntries 5000: %d entries, truncated %v; want 5000, true", len(bounded.Entries), bounded.Truncated)
	}
}

// list-files.ts:30-34 checks maxEntries, maxBytes and timeoutMs in that order before it asks the daemon, so the first invalid
// one names the error, and each invalid value of Number.isSafeInteger or a negative one is refused.
func TestListFilesNamesTheFirstInvalidBoundInPisOrder(t *testing.T) {
	t.Parallel()
	bad, fraction, unsafe, good := -1.0, 1.5, 9_007_199_254_740_992.0, 5.0
	for _, test := range []struct {
		options durableenv.ListFilesOptions
		want    string
	}{
		{durableenv.ListFilesOptions{MaxEntries: &bad, MaxBytes: &bad, TimeoutMs: &bad}, "maxEntries"},
		{durableenv.ListFilesOptions{MaxEntries: &good, MaxBytes: &fraction, TimeoutMs: &bad}, "maxBytes"},
		{durableenv.ListFilesOptions{MaxEntries: &good, MaxBytes: &good, TimeoutMs: &unsafe}, "timeoutMs"},
	} {
		// Repeated: a check that walked the bounds in an unordered way would name another one on some runs.
		for range 50 {
			_, err := listFilesOver(t.Context(), nil, "/listed", &test.options)
			var fileError *durableenv.FileError
			if !errors.As(err, &fileError) || fileError.Code != durableenv.FileErrorInvalid || fileError.Message != test.want+" must be a non-negative safe integer" || fileError.Path != "/listed" {
				t.Fatalf("listFilesOver = %v; want invalid %q naming /listed", err, test.want+" must be a non-negative safe integer")
			}
		}
	}
}
