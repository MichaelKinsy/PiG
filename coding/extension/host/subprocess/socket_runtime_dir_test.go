package subprocess

import (
	"fmt"
	"math"
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

// A socket directory two bytes deeper than the directory the e-<counter>.sock leaf (24 bytes with its <prefix><uint32> directory) kept is kept now that the longest leaf is 9 bytes (22 below the directory), and a directory one byte deeper than that falls back: so the branch changes only for a directory whose length is the limit less 23 or less 22, and neither a default Linux directory (/run/user/<uid>/pig, about 20 bytes) nor a default macOS one (/var/folders/xx/<30 bytes>/T/pig-<uid>, about 60 bytes) is near them.
func TestSocketRuntimeBaseBranchAtTheEdgeOfTheShorterLeaf(t *testing.T) {
	if worstRuntimeSocketSuffix != 22 {
		t.Fatalf("worstRuntimeSocketSuffix = %d, the rows below name the lengths for 22", worstRuntimeSocketSuffix)
	}
	for _, goos := range []string{"linux", "darwin"} {
		limit := unixSocketPathLimit(goos)
		for _, test := range []struct {
			length int
			kept   bool
		}{
			{limit - 24, true}, // kept before and now
			{limit - 23, true}, // fell back before the shorter leaf
			{limit - 22, true}, // fell back before the shorter leaf
			{limit - 21, false},
			{limit - 20, false},
		} {
			dir := "/" + strings.Repeat("d", test.length-1)
			got := socketRuntimeBase(goos, dir, 501, "")
			if kept := got == dir; kept != test.kept || (!kept && got != "/tmp/pig-501") {
				t.Errorf("socketRuntimeBase(%s, a %d-byte directory (limit %d)) = %q, want kept = %v", goos, test.length, limit, got, test.kept)
			}
			if test.kept {
				if err := validateUnixSocketPath(goos, dir+"/"+socketRuntimeDirPrefix+"4294967295/"+worstRuntimeSocketLeaf); err != nil {
					t.Errorf("%s: a kept %d-byte directory does not hold the longest socket path: %v", goos, test.length, err)
				}
			}
		}
	}
}

// oldWorstRuntimeSocketSuffix is what the socket directory budget was before the shorter leaf: os.MkdirTemp's h<uint32> directory and an e-<counter>.sock leaf of up to four digits (24 bytes).
const oldWorstRuntimeSocketSuffix = len("/h4294967295/e-9999.sock")

// A $TMPDIR makes the socket directory $TMPDIR/pig-<uid>, 15 bytes below it for the widest uid (ten digits). Whether the socket directory is kept is a length test against the limit less the worst suffix, so the shorter leaf moves the edge by two bytes and changes the branch of a $TMPDIR of two lengths only: on Linux (limit 107, edge 83 before and 85 now) 68 bytes is kept before and now, 69 and 70 bytes fell back to /tmp/pig-<uid> before and are kept now, and 71 bytes falls back both times; on macOS (limit 103) the same rows are 64, 65 and 66, and 67. A $TMPDIR of any other length is on the branch it was on.
func TestATempDirOf69Or70BytesIsKeptNowAndFellBackBefore(t *testing.T) {
	if oldWorstRuntimeSocketSuffix != 24 || worstRuntimeSocketSuffix != 22 {
		t.Fatalf("the suffixes are %d before and %d now, the rows name the lengths for 24 and 22", oldWorstRuntimeSocketSuffix, worstRuntimeSocketSuffix)
	}
	const widestUID = math.MaxInt32 // ten digits, as the widest uid is
	for _, test := range []struct {
		goos       string
		tmpLen     int
		keptBefore bool
		keptNow    bool
		socketLen  int
	}{
		{"linux", 67, true, true, 82},
		{"linux", 68, true, true, 83},
		{"linux", 69, false, true, 84},
		{"linux", 70, false, true, 85},
		{"linux", 71, false, false, 86},
		{"darwin", 63, true, true, 78},
		{"darwin", 64, true, true, 79},
		{"darwin", 65, false, true, 80},
		{"darwin", 66, false, true, 81},
		{"darwin", 67, false, false, 82},
	} {
		tmp := "/" + strings.Repeat("t", test.tmpLen-1)
		dir := resolveSocketDirForGOOS(test.goos, "", tmp, "/unused", widestUID)
		if len(dir) != test.socketLen {
			t.Fatalf("%s: a %d-byte $TMPDIR gives a %d-byte socket directory %q, want %d", test.goos, test.tmpLen, len(dir), dir, test.socketLen)
		}
		limit := unixSocketPathLimit(test.goos)
		if before := len(dir)+oldWorstRuntimeSocketSuffix <= limit; before != test.keptBefore {
			t.Errorf("%s: a %d-byte $TMPDIR was kept before = %v, the row says %v", test.goos, test.tmpLen, before, test.keptBefore)
		}
		got := socketRuntimeBase(test.goos, dir, widestUID, "")
		if kept := got == dir; kept != test.keptNow || (!kept && got != fmt.Sprintf("/tmp/pig-%d", widestUID)) {
			t.Errorf("%s: a %d-byte $TMPDIR gives the socket base %q for the directory %q, want kept = %v", test.goos, test.tmpLen, got, dir, test.keptNow)
		}
	}
}

// The directories a default user gets are far from every edge, so they are on the branch they were on: macOS's $TMPDIR is /var/folders/<2 bytes>/<30 bytes>/T/ (49 bytes with the slash it ends in; `getconf DARWIN_USER_TEMP_DIR` on macOS 26 prints /var/folders/_l/n7y26ltj6bzf554x9_k3b0w80000gp/T/), so the socket directory is 56 bytes for uid 501 and 63 for the widest uid, against the 79 the old leaf allowed; Linux's is $XDG_RUNTIME_DIR/pig, or $TMPDIR/pig-<uid>.
func TestADefaultUsersSocketDirectoryIsKeptBeforeAndNow(t *testing.T) {
	const macTemp = "/var/folders/_l/n7y26ltj6bzf554x9_k3b0w80000gp/T/"
	if len(macTemp) != 49 {
		t.Fatalf("the default macOS temp directory is %d bytes, the row says 49", len(macTemp))
	}
	for _, test := range []struct {
		goos, xdg, tmp string
		uid            int
		socketLen      int
	}{
		{"darwin", "", macTemp, 501, 56},
		{"darwin", "", macTemp, math.MaxInt32, 63},
		{"linux", "/run/user/1000", "", 1000, 18},
		{"linux", "/run/user/2147483647", "", math.MaxInt32, 24},
		{"linux", "", "/tmp", 1000, 13},
	} {
		dir := resolveSocketDirForGOOS(test.goos, test.xdg, test.tmp, "/tmp", test.uid)
		if len(dir) != test.socketLen {
			t.Fatalf("%s: the default socket directory %q is %d bytes, want %d", test.goos, dir, len(dir), test.socketLen)
		}
		if len(dir)+oldWorstRuntimeSocketSuffix > unixSocketPathLimit(test.goos) {
			t.Errorf("%s: the default socket directory %q was not kept before", test.goos, dir)
		}
		if got := socketRuntimeBase(test.goos, dir, test.uid, ""); got != dir {
			t.Errorf("%s: the default socket directory %q is not kept now: %q", test.goos, dir, got)
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

// Without a local application data directory a deep Windows temp directory stays the base, and newSocketPath reports
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
		worst := dir + `\h4294967295\` + worstRuntimeSocketLeaf
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
	if got := len(sep + name + sep + worstRuntimeSocketLeaf); got > worstRuntimeSocketSuffix {
		t.Fatalf("runtime directory %q gives a %d-byte socket suffix; worstRuntimeSocketSuffix is %d", name, got, worstRuntimeSocketSuffix)
	}
}

// A $TMPDIR deep enough that <socketDir>/h<N>/<count>.s exceeds the Unix socket limit still leaves a working Host: the
// extension process cannot start when its socket path is too long, and Pi's in-process extensions have no such limit.
func TestNewSocketPathUsesAShortDirectoryWhenTheSocketDirectoryIsDeep(t *testing.T) {
	if runtime.GOOS == "windows" && os.Getenv("LOCALAPPDATA") == "" {
		t.Skip("the Windows fallback socket directory needs LOCALAPPDATA")
	}
	deep := filepath.Join(t.TempDir(), strings.Repeat("d", unixSocketPathLimit(runtime.GOOS)))
	host := &Host{socketDir: deep}
	path, err := host.newSocketPath()
	if err != nil {
		t.Fatalf("newSocketPath under a %d-byte socket directory: %v", len(deep), err)
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
