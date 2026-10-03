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
		{"windows", `C:\Users\runner\AppData\Local\Temp\pig`},
		{"windows", `D:\` + strings.Repeat("d", unixSocketPathLimit("windows")-worstRuntimeSocketSuffix-3)},
	} {
		if got := socketRuntimeBase(test.goos, test.dir, 501, `C:\Users\runner\AppData\Local`); got != test.dir {
			t.Errorf("socketRuntimeBase(%s, %q) = %q, want the directory kept", test.goos, test.dir, got)
		}
	}
}

func TestSocketRuntimeBaseFallsBackWhenASocketPathCouldExceedTheLimit(t *testing.T) {
	const localAppData = `C:\Users\runner\AppData\Local`
	for _, test := range []struct {
		goos, dir, want string
	}{
		{"linux", "/" + strings.Repeat("d", unixSocketPathLimit("linux")-worstRuntimeSocketSuffix), "/tmp/pig-501"},
		{"darwin", "/" + strings.Repeat("d", unixSocketPathLimit("darwin")-worstRuntimeSocketSuffix), "/tmp/pig-501"},
		// The CI geometry: a TEMP with spaces, a scoped test directory, and a test-named directory below it.
		{"windows", `D:\a\_temp\pig extension acceptance\pig-sp-375215422\pig-model-presence-2689393714\pig`, localAppData + `\pig\s`},
		{"windows", `D:\` + strings.Repeat("d", unixSocketPathLimit("windows")-worstRuntimeSocketSuffix-2), localAppData + `\pig\s`},
	} {
		got := socketRuntimeBase(test.goos, test.dir, 501, localAppData)
		if test.goos == "windows" {
			got = strings.ReplaceAll(got, "/", `\`)
		}
		if got != test.want {
			t.Errorf("socketRuntimeBase(%s, a %d-byte directory) = %q, want %q", test.goos, len(test.dir), got, test.want)
		}
	}
}

// Without a local application data directory a deep Windows temp directory stays the base, and sockPathFor reports
// the over-long socket path rather than binding somewhere unintended.
func TestSocketRuntimeBaseWindowsWithoutLocalAppDataKeepsTheDirectory(t *testing.T) {
	dir := `D:\` + strings.Repeat("d", 120)
	if got := socketRuntimeBase("windows", dir, 0, ""); got != dir {
		t.Fatalf("socketRuntimeBase = %q, want %q", got, dir)
	}
}

// The worst-case socket path below a base that socketRuntimeBase keeps is within the platform limit, for the Windows
// CI geometry of TestWindowsUnixSocketPathsAreShortIsolatedAndCleaned with the longest suffixes MkdirTemp can produce
// (a uint32 in decimal): every real directory name stays at or under the worst case.
func TestSocketRuntimeBaseKeptDirectoryHoldsEveryRuntimeSocketPath(t *testing.T) {
	for _, dir := range []string{
		`D:\a\_temp\pig extension acceptance\pig-sp-4294967295\pig ext 4294967295\pig`,
	} {
		if got := socketRuntimeBase("windows", dir, 0, `C:\L`); got != dir {
			t.Fatalf("socketRuntimeBase fell back for %q: %q", dir, got)
		}
		worst := dir + `\h4294967295\e-9999.sock`
		if err := validateUnixSocketPath("windows", worst); err != nil {
			t.Fatal(err)
		}
	}
}

// The runtime directory a Host creates is no longer than the one worstRuntimeSocketSuffix budgets for, so a base that
// socketRuntimeBase keeps holds every socket path the Host can produce.
func TestEnsureSockRuntimeDirNameFitsTheWorstCaseSuffix(t *testing.T) {
	host := &Host{socketDir: t.TempDir()}
	dir, err := host.ensureSockRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sep := string(filepath.Separator)
	name := filepath.Base(dir)
	if !strings.HasPrefix(name, socketRuntimeDirPrefix) {
		t.Fatalf("runtime directory %q lacks the prefix %q", name, socketRuntimeDirPrefix)
	}
	if got := len(sep + name + sep + "e-9999.sock"); got > worstRuntimeSocketSuffix {
		t.Fatalf("runtime directory %q gives a %d-byte socket suffix; worstRuntimeSocketSuffix is %d", name, got, worstRuntimeSocketSuffix)
	}
}

// A $TMPDIR deep enough that <socketDir>/h<N>/e-0.sock exceeds the Unix socket limit still leaves a working Host: the
// extension process cannot start when its socket path is too long, and Pi's in-process extensions have no such limit.
func TestSockPathForUsesAShortDirectoryWhenTheSocketDirectoryIsDeep(t *testing.T) {
	if runtime.GOOS == "windows" && os.Getenv("LOCALAPPDATA") == "" {
		t.Skip("the Windows fallback socket directory needs LOCALAPPDATA")
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
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o700) {
		t.Fatalf("runtime directory %q: %v, %v", filepath.Dir(path), info, err)
	}
}

// The fallback base /tmp/pig-<uid> sits in a shared directory: a base another user could write lets that user rename
// the Host's private runtime directory and substitute its own before the Host binds its sockets. A Host takes a base
// only when it is a real directory it owns, and makes an owned base private again.
func TestEnsureSockRuntimeDirRequiresAPrivateOwnedBase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("owner and mode checks are Unix-only; Windows accepts its per-user directory as is")
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
	testenv.RequireDirectoryLink(t, target, link)
	linked := &Host{socketDir: link}
	if dir, err := linked.ensureSockRuntimeDir(); err == nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("a symlinked base was accepted: runtime directory %q", dir)
	}
}
