package env

import (
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// ssh.ts localDaemon maps Node's process.platform and process.arch to the packaged daemon of the machine it runs on, and throws for a system the package ships no daemon for: "No pi-env daemon for <platform>" with Node's spelling of the platform, then normalizeArch's "Unsupported remote architecture: <arch>" with Node's spelling of the architecture.
func TestLocalDaemonSystems(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "daemons")
	t.Setenv("PI_ENV_DAEMON_DIR", directory)
	for _, test := range []struct {
		goos, goarch string
		want         string
		wantErr      string
	}{
		{"linux", "amd64", filepath.Join(directory, "pi-env-linux-x64", "pi-env"), ""},
		{"linux", "arm64", filepath.Join(directory, "pi-env-linux-arm64", "pi-env"), ""},
		{"android", "arm64", filepath.Join(directory, "pi-env-android-arm64", "pi-env"), ""},
		{"darwin", "arm64", filepath.Join(directory, "pi-env-darwin-arm64", "pi-env"), ""},
		{"darwin", "amd64", filepath.Join(directory, "pi-env-darwin-x64", "pi-env"), ""},
		{"windows", "amd64", filepath.Join(directory, "pi-env-windows-x64", "pi-env.exe"), ""},
		{"windows", "arm64", filepath.Join(directory, "pi-env-windows-arm64", "pi-env.exe"), ""},
		{"freebsd", "amd64", "", "No pi-env daemon for freebsd"},
		{"solaris", "amd64", "", "No pi-env daemon for sunos"},
		{"linux", "386", "", "Unsupported remote architecture: ia32"},
		{"linux", "riscv64", "", "Unsupported remote architecture: riscv64"},
		{"windows", "386", "", "Unsupported remote architecture: ia32"},
		{"linux", "mipsle", "", "Unsupported remote architecture: mipsel"},
		{"linux", "ppc64le", "", "Unsupported remote architecture: ppc64"},
		{"freebsd", "386", "", "No pi-env daemon for freebsd"},
	} {
		got, err := localDaemonFor(test.goos, test.goarch)
		if test.wantErr != "" {
			if err == nil || err.Error() != test.wantErr {
				t.Errorf("localDaemonFor(%q, %q) = %q, %v; want error %q", test.goos, test.goarch, got, err, test.wantErr)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Errorf("localDaemonFor(%q, %q) = %q, %v; want %q", test.goos, test.goarch, got, err, test.want)
		}
	}
}

// LocalDaemon resolves the daemon of the machine the test runs on: Node's process.platform and process.arch of that machine name the pi-env-<platform>-<arch> directory.
func TestLocalDaemonRunningSystem(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("PI_ENV_DAEMON_DIR", directory)
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	file := "pi-env"
	if runtime.GOOS == "windows" {
		file = "pi-env.exe"
	}
	got, err := LocalDaemon()
	if arch == "" || !slices.Contains([]string{"linux", "android", "darwin", "windows"}, runtime.GOOS) {
		if err == nil {
			t.Errorf("LocalDaemon() on %s/%s = %q, want an error", runtime.GOOS, runtime.GOARCH, got)
		}
		return
	}
	want := filepath.Join(directory, "pi-env-"+runtime.GOOS+"-"+arch, file)
	if err != nil || got != want {
		t.Errorf("LocalDaemon() = %q, %v; want %q", got, err, want)
	}
}
