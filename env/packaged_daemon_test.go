package env

import (
	"os"
	"path/filepath"
	"testing"
)

// The daemon of a remote system is found under PI_ENV_DAEMON_DIR when it is set, and otherwise in the pi-env directory next to the running executable (D97). Pi resolves it inside its npm package.
func TestPackagedDaemonLocation(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "daemons")
	t.Setenv("PI_ENV_DAEMON_DIR", directory)
	for _, test := range []struct {
		remote RemotePlatform
		want   string
	}{
		{RemotePlatform{Platform: "linux", Arch: "x64"}, filepath.Join(directory, "pi-env-linux-x64", "pi-env")},
		{RemotePlatform{Platform: "darwin", Arch: "arm64"}, filepath.Join(directory, "pi-env-darwin-arm64", "pi-env")},
		{RemotePlatform{Platform: "windows", Arch: "x64"}, filepath.Join(directory, "pi-env-windows-x64", "pi-env.exe")},
	} {
		if got := PackagedDaemon(test.remote); got != test.want {
			t.Errorf("PackagedDaemon(%+v) = %q, want %q", test.remote, got, test.want)
		}
	}
	t.Setenv("PI_ENV_DAEMON_DIR", "")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(executable), "pi-env", "pi-env-linux-arm64", "pi-env")
	if got := PackagedDaemon(RemotePlatform{Platform: "linux", Arch: "arm64"}); got != want {
		t.Errorf("default PackagedDaemon = %q, want %q", got, want)
	}
}
