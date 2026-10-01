package subprocess

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestSocketRuntimeBaseKeepsADirectoryWhoseSocketsFit(t *testing.T) {
	for _, test := range []struct {
		goos string
		dir  string
	}{
		{"linux", "/run/user/1000/pig"},
		{"darwin", "/var/folders/zz/zyxvpxvq6csfxvn_n0000000000000/T/pig-501"},
		{"linux", "/" + strings.Repeat("d", unixSocketPathLimit("linux")-worstRuntimeSocketSuffix-1)},
		{"darwin", "/" + strings.Repeat("d", unixSocketPathLimit("darwin")-worstRuntimeSocketSuffix-1)},
		{"windows", `C:\Users\runner\AppData\Local\Temp\` + strings.Repeat("d", 200) + `\pig`},
	} {
		if got := socketRuntimeBase(test.goos, test.dir, 501); got != test.dir {
			t.Errorf("socketRuntimeBase(%s, %q) = %q, want the directory kept", test.goos, test.dir, got)
		}
	}
}

func TestSocketRuntimeBaseFallsBackWhenASocketPathCouldExceedTheLimit(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		dir := "/" + strings.Repeat("d", unixSocketPathLimit(goos)-worstRuntimeSocketSuffix)
		if got, want := socketRuntimeBase(goos, dir, 501), "/tmp/pig-501"; got != want {
			t.Errorf("socketRuntimeBase(%s, a %d-byte directory) = %q, want %q", goos, len(dir), got, want)
		}
	}
}

// A $TMPDIR deep enough that <socketDir>/host-N/e-0.sock exceeds the Unix socket limit still leaves a working Host: the
// extension process cannot start when its socket path is too long, and Pi's in-process extensions have no such limit.
func TestSockPathForUsesAShortDirectoryWhenTheSocketDirectoryIsDeep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps its temp-directory socket path")
	}
	deep := filepath.Join(t.TempDir(), strings.Repeat("d", unixSocketPathLimit(runtime.GOOS)))
	host := &Host{socketDir: deep}
	path, err := host.sockPathFor("deep")
	if err != nil {
		t.Fatalf("sockPathFor under a %d-byte socket directory: %v", len(deep), err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(host.sockRuntimeDir) })
	if len(path) > unixSocketPathLimit(runtime.GOOS) {
		t.Fatalf("socket path has %d bytes: %q", len(path), path)
	}
	if strings.HasPrefix(path, deep) {
		t.Fatalf("socket path %q stayed under the deep directory", path)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen on %q: %v", path, err)
	}
	_ = listener.Close()
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime directory %q: %v, %v", filepath.Dir(path), info, err)
	}
}

// The fallback base /tmp/pig-<uid> sits in a shared directory: a base another user could write lets that user rename
// the Host's private runtime directory and substitute its own before the Host binds its sockets. A Host takes a base
// only when it is a real directory it owns, and makes an owned base private again.
func TestEnsureSockRuntimeDirRequiresAPrivateOwnedBase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps its per-user temp directory")
	}
	// A short base under /tmp, so socketRuntimeBase keeps it rather than falling back.
	parent, err := os.MkdirTemp("/tmp", "pig-base-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	wide := filepath.Join(parent, "wide")
	if err := os.Mkdir(wide, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(wide, 0o777); err != nil {
		t.Fatal(err)
	}
	host := &Host{socketDir: wide}
	dir, err := host.ensureSockRuntimeDir()
	if err != nil {
		t.Fatalf("an owned base with wide permissions: %v", err)
	}
	if filepath.Dir(dir) != wide {
		t.Fatalf("runtime directory %q is not below the base %q", dir, wide)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if info, err := os.Stat(wide); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("base %q after use: %v, %v; want mode 0700", wide, info, err)
	}

	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	testenv.Symlink(t, target, link)
	linked := &Host{socketDir: link}
	if dir, err := linked.ensureSockRuntimeDir(); err == nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("a symlinked base was accepted: runtime directory %q", dir)
	}
}
