//go:build !android

// execveErrno applies the kernel's binary format rules only where GOOS is linux; Android starts every executable file.

package nodespawn

import (
	"bytes"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// fakeBinfmtMisc points binfmtMiscDir at a directory holding status and the given entries, each written as entry_status (fs/binfmt_misc.c) prints it, in name order.
func fakeBinfmtMisc(t *testing.T, status string, entries map[string]string) {
	t.Helper()
	fakeBinfmtMiscInOrder(t, status, slices.Sorted(maps.Keys(entries)), entries)
}

// fakeBinfmtMiscInOrder is fakeBinfmtMisc with the entries created in the order of names.
func fakeBinfmtMiscInOrder(t *testing.T, status string, names []string, entries map[string]string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("status", status+"\n")
	write("register", "")
	for _, name := range names {
		write(name, entries[name])
	}
	previous := binfmtMiscDir
	binfmtMiscDir = dir
	t.Cleanup(func() { binfmtMiscDir = previous })
}

// writeProgram writes an executable file with content.
func writeProgram(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseBinfmtMiscEntryReadsEntryStatus(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry string
		want  binfmtMiscHandler
		ok    bool
	}{
		{"magic with fixed interpreter", "enabled\ninterpreter /init\nflags: PF\noffset 0\nmagic 4d5a\n",
			binfmtMiscHandler{interpreter: "/init", fixBinary: true, magic: []byte("MZ")}, true},
		{"magic with mask and offset", "enabled\ninterpreter /usr/bin/qemu\nflags: OCF\noffset 2\nmagic 0102\nmask ff0f\n",
			binfmtMiscHandler{interpreter: "/usr/bin/qemu", fixBinary: true, openBinary: true, offset: 2, magic: []byte{1, 2}, mask: []byte{0xff, 0x0f}}, true},
		{"extension", "enabled\ninterpreter /usr/bin/wine\nflags: \nextension .exe\n",
			binfmtMiscHandler{interpreter: "/usr/bin/wine", extension: "exe"}, true},
		{"open binary", "enabled\ninterpreter /usr/bin/run\nflags: O\nextension .run\n",
			binfmtMiscHandler{interpreter: "/usr/bin/run", openBinary: true, extension: "run"}, true},
		{"disabled", "disabled\ninterpreter /init\nflags: F\noffset 0\nmagic 4d5a\n", binfmtMiscHandler{}, false},
		{"malformed magic", "enabled\ninterpreter /init\nflags: \noffset 0\nmagic 4z\n", binfmtMiscHandler{}, false},
		{"magic past the head", "enabled\ninterpreter /init\nflags: \noffset 255\nmagic 4d5a\n", binfmtMiscHandler{}, false},
		{"mask of another length", "enabled\ninterpreter /init\nflags: \noffset 0\nmagic 4d5a\nmask ff\n", binfmtMiscHandler{}, false},
		{"no interpreter", "enabled\nflags: \noffset 0\nmagic 4d5a\n", binfmtMiscHandler{}, false},
		{"flags without F", "enabled\ninterpreter /init\nflags: P\noffset 0\nmagic 4d5a\n",
			binfmtMiscHandler{interpreter: "/init", magic: []byte("MZ")}, true},
		{"magic ending at the end of the head", "enabled\ninterpreter /init\nflags: \noffset 254\nmagic 4d5a\n",
			binfmtMiscHandler{interpreter: "/init", offset: 254, magic: []byte("MZ")}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseBinfmtMiscEntry([]byte(tc.entry))
			if ok != tc.ok || (ok && (got.interpreter != tc.want.interpreter || got.fixBinary != tc.want.fixBinary || got.openBinary != tc.want.openBinary ||
				got.extension != tc.want.extension || got.offset != tc.want.offset || !bytes.Equal(got.magic, tc.want.magic) || !bytes.Equal(got.mask, tc.want.mask))) {
				t.Fatalf("parseBinfmtMiscEntry = %+v, %v; want %+v, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// The kernel tries the enabled binfmt_misc handlers before the "#!" and ELF handlers (insert_binfmt, fs/binfmt_misc.c), so a file one of them recognizes, such as a Windows program under WSL's WSLInterop handler (magic MZ, flag F), starts instead of failing with ENOEXEC, and execvp does not run it with /bin/sh.
func TestExecveErrnoFollowsBinfmtMiscHandlers(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	pe := writeProgram(t, dir, "tool.exe", "MZ\x90\x00windows program")
	byExtension := writeProgram(t, dir, "data.foo", "no magic")
	masked := writeProgram(t, dir, "masked", "xx\x01\x52rest")
	disabled := writeProgram(t, dir, "disabled", "ZZ disabled handler")
	plain := writeProgram(t, dir, "plain", "no handler")
	// The "#!" handler alone would fail with ENOENT for this missing interpreter; the binfmt_misc handler for "#!/x" is tried first.
	script := writeProgram(t, dir, "script", "#!/x/missing-interpreter\n")
	// The extension is the text after the last ".", so an extension with a "." never matches.
	archive := writeProgram(t, dir, "x.tar.gz", "no magic")
	fakeBinfmtMisc(t, "enabled", map[string]string{
		"WSLInterop": "enabled\ninterpreter /nonexistent/init\nflags: F\noffset 0\nmagic 4d5a\n",
		"hashbang":   "enabled\ninterpreter /nonexistent/runner\nflags: F\noffset 0\nmagic 23212f78\n",
		"foo":        "enabled\ninterpreter " + self + "\nflags: \nextension .foo\n",
		"qemu":       "enabled\ninterpreter /nonexistent/qemu\nflags: \noffset 2\nmagic 0102\nmask ff0f\n",
		"off":        "disabled\ninterpreter /nonexistent/init\nflags: F\noffset 0\nmagic 5a5a\n",
		"archive":    "enabled\ninterpreter /nonexistent/tar\nflags: F\nextension .tar.gz\n",
	})
	for _, tc := range []struct {
		path string
		want syscall.Errno
	}{
		{pe, 0},
		{byExtension, 0},
		{masked, syscall.ENOENT},
		{disabled, syscall.ENOEXEC},
		{plain, syscall.ENOEXEC},
		{script, 0},
		{archive, syscall.ENOEXEC},
	} {
		if got := execveErrno(tc.path, dir, 0); got != tc.want {
			t.Errorf("execveErrno(%s) = %v, want %v", filepath.Base(tc.path), got, tc.want)
		}
	}

	cmd := exec.Command(pe, "/c", "echo x")
	SetProgram(cmd)
	if cmd.Err != nil || cmd.Path != pe || strings.Join(cmd.Args, " ") != pe+" /c echo x" {
		t.Errorf("SetProgram(tool.exe) = %q %q, %v; want the program itself", cmd.Path, cmd.Args, cmd.Err)
	}
	cmd = exec.Command(plain)
	SetProgram(cmd)
	if cmd.Path != "/bin/sh" {
		t.Errorf("SetProgram(plain) path = %q, want /bin/sh", cmd.Path)
	}
}

// The first enabled handler in the directory's order claims a file that two handlers recognize. The fake directory's own order stands in for the kernel's, which lists the newest entry first.
func TestBinfmtMiscHandlersAreTriedInDirectoryOrder(t *testing.T) {
	dir := t.TempDir()
	program := writeProgram(t, dir, "two.exe", "R15A program")
	starts := "enabled\ninterpreter /nonexistent/a\nflags: F\noffset 0\nmagic 52313541\n"
	missing := "enabled\ninterpreter /nonexistent/b\nflags: \noffset 0\nmagic 52313541\n"
	// The handlers give different results. Names and creation orders change until the directory lists the handlers out of name order, so that a reversed listing and a listing sorted by name both give the other result.
	var want syscall.Errno
	for attempt := range 16 {
		first, second := "a"+strconv.Itoa(attempt), "b"+strconv.Itoa(attempt)
		created := []string{first, second}
		if attempt%2 == 1 {
			created = []string{second, first}
		}
		fakeBinfmtMiscInOrder(t, "enabled", created, map[string]string{first: starts, second: missing})
		listing, err := os.Open(binfmtMiscDir)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := listing.ReadDir(-1)
		_ = listing.Close()
		if err != nil {
			t.Fatal(err)
		}
		entries = slices.DeleteFunc(entries, func(entry os.DirEntry) bool { return entry.Name() == "status" || entry.Name() == "register" })
		if want = 0; entries[0].Name() == second {
			want = syscall.ENOENT
			break
		}
	}
	if got := execveErrno(program, dir, 0); got != want {
		t.Errorf("execveErrno = %v, want %v from the first handler in directory order", got, want)
	}
}

// The kernel compares (byte ^ magic) & mask, and reads the head into a zeroed buffer of BINPRM_BUF_SIZE bytes, so a magic past the end of a short file compares with zeros.
func TestBinfmtMiscMagicIsMaskedAndZeroPadded(t *testing.T) {
	dir := t.TempDir()
	low := writeProgram(t, dir, "low", "\x0frest of the file")
	high := writeProgram(t, dir, "high", "\x10rest of the file")
	short := writeProgram(t, dir, "short", "MZ")
	fakeBinfmtMisc(t, "enabled", map[string]string{
		"low":   "enabled\ninterpreter /nonexistent/a\nflags: F\noffset 0\nmagic ff\nmask 0f\n",
		"zeros": "enabled\ninterpreter /nonexistent/b\nflags: F\noffset 10\nmagic 0000\n",
	})
	for _, tc := range []struct {
		path string
		want syscall.Errno
	}{{low, 0}, {short, 0}, {high, syscall.ENOEXEC}} {
		if got := execveErrno(tc.path, dir, 0); got != tc.want {
			t.Errorf("execveErrno(%s) = %v, want %v", filepath.Base(tc.path), got, tc.want)
		}
	}
}

// The kernel runs a handler's interpreter one level deeper, through every handler again (exec_binprm, fs/exec.c): a handler loop fails with ELOOP, and so does one whose interpreter it opened at registration (flag F). That interpreter must itself be a program it can start, and one without flag F is opened in the child's working directory, as a "#!" interpreter.
func TestExecveErrnoRunsTheBinfmtMiscInterpreter(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	loop := writeProgram(t, dir, "x.loop", "looping data")
	fixedLoop := writeProgram(t, dir, "fixed-loop", "LOOP fixed interpreter of itself")
	text := writeProgram(t, dir, "text-interpreter", "an executable text file")
	textData := writeProgram(t, dir, "text-data", "TEXT data")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	writeProgram(t, dir, "sub/interpreter", "#!"+self+"\n")
	writeProgram(t, dir, "interpreter", "#!"+self+"\n")
	subData := writeProgram(t, dir, "data.sub", "data")
	bareData := writeProgram(t, dir, "data.bare", "data")
	// Scripts that end in a file whose flag F interpreter is missing: that interpreter is the sixth, one too many, after five scripts, and the fifth after four.
	fixedMissing := writeProgram(t, dir, "fixed-missing", "MISS")
	chain := func(name string, scripts int) string {
		next := fixedMissing
		for i := scripts; i >= 1; i-- {
			next = writeProgram(t, dir, name+strconv.Itoa(i), "#!"+next+"\n")
		}
		return next
	}
	fiveScripts, fourScripts := chain("five", 5), chain("four", 4)
	fakeBinfmtMisc(t, "enabled", map[string]string{
		"missing":   "enabled\ninterpreter /nonexistent/fixed\nflags: F\noffset 0\nmagic 4d495353\n",
		"loop":      "enabled\ninterpreter " + loop + "\nflags: \nextension .loop\n",
		"fixedLoop": "enabled\ninterpreter " + fixedLoop + "\nflags: F\noffset 0\nmagic 4c4f4f50\n",
		"text":      "enabled\ninterpreter " + text + "\nflags: F\noffset 0\nmagic 54455854\n",
		"sub":       "enabled\ninterpreter sub/interpreter\nflags: \nextension .sub\n",
		"bare":      "enabled\ninterpreter interpreter\nflags: \nextension .bare\n",
	})
	for _, tc := range []struct {
		path string
		want syscall.Errno
	}{
		{loop, syscall.ELOOP},
		{fixedLoop, syscall.ELOOP},
		{textData, syscall.ENOEXEC},
		{subData, 0},
		{fiveScripts, syscall.ELOOP},
		{fourScripts, 0},
	} {
		if got := execveErrno(tc.path, dir, 0); got != tc.want {
			t.Errorf("execveErrno(%s) = %v, want %v", filepath.Base(tc.path), got, tc.want)
		}
	}
	// An interpreter without a slash is a file in the working directory, not a program in the PATH.
	t.Chdir(dir)
	if got := execveErrno(bareData, "", 0); got != 0 {
		t.Errorf("execveErrno(data.bare) in its directory = %v, want 0", got)
	}
}

// A handler with flag O, which flag C implies, passes the file to its interpreter as an open descriptor, and exec_binprm (fs/exec.c) then fails with ENOEXEC when a later level sets an interpreter again: a "#!" interpreter or one that another binfmt_misc handler claims. The interpreter's own open error comes first, and an ELF interpreter starts.
func TestExecveErrnoStopsAtAnInterpreterAfterAnOpenBinaryHandler(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := writeProgram(t, dir, "script-interpreter", "#!"+self+"\n")
	claimed := writeProgram(t, dir, "claimed-interpreter", "CLAIMED by another handler")
	data := func(name string) string { return writeProgram(t, dir, name, "data") }
	scriptAfterOpen, scriptAfterFixedOpen, scriptAfterCredentials := data("x.os"), data("x.ofs"), data("x.cs")
	missingAfterOpen, claimedAfterOpen, elfAfterOpen := data("x.om"), data("x.ocl"), data("x.oe")
	missingScript := writeProgram(t, dir, "missing-script", "#!/nonexistent/sh\n")
	missingScriptAfterOpen := data("x.osm")
	// An open binary handler deeper in the chain is fine: the "#!" interpreter before it was set while no handler had flag O.
	scriptBeforeOpen := writeProgram(t, dir, "script-before", "#!"+elfAfterOpen+"\n")
	twoScripts := writeProgram(t, dir, "two-scripts", "#!"+script+"\n")
	// Four scripts, then an open binary handler's file at depth 4, whose interpreter at depth 5 a flag F handler claims: exec_binprm fails with ENOEXEC in that iteration, before the depth check of the next one.
	deepest := claimedAfterOpen
	for i := 4; i >= 1; i-- {
		deepest = writeProgram(t, dir, "deep"+strconv.Itoa(i), "#!"+deepest+"\n")
	}
	fakeBinfmtMisc(t, "enabled", map[string]string{
		"os":      "enabled\ninterpreter " + script + "\nflags: O\nextension .os\n",
		"ofs":     "enabled\ninterpreter " + script + "\nflags: OF\nextension .ofs\n",
		"cs":      "enabled\ninterpreter " + script + "\nflags: OC\nextension .cs\n",
		"om":      "enabled\ninterpreter /nonexistent/run\nflags: O\nextension .om\n",
		"osm":     "enabled\ninterpreter " + missingScript + "\nflags: O\nextension .osm\n",
		"ocl":     "enabled\ninterpreter " + claimed + "\nflags: O\nextension .ocl\n",
		"oe":      "enabled\ninterpreter " + self + "\nflags: O\nextension .oe\n",
		"claimed": "enabled\ninterpreter /nonexistent/claimer\nflags: F\noffset 0\nmagic 434c41494d4544\n",
	})
	for _, tc := range []struct {
		path string
		want syscall.Errno
	}{
		{scriptAfterOpen, syscall.ENOEXEC},
		{scriptAfterFixedOpen, syscall.ENOEXEC},
		{scriptAfterCredentials, syscall.ENOEXEC},
		{missingAfterOpen, syscall.ENOENT},
		{missingScriptAfterOpen, syscall.ENOENT},
		{claimedAfterOpen, syscall.ENOEXEC},
		{elfAfterOpen, 0},
		{scriptBeforeOpen, 0},
		{twoScripts, 0},
		{deepest, syscall.ENOEXEC},
	} {
		if got := execveErrno(tc.path, dir, 0); got != tc.want {
			t.Errorf("execveErrno(%s) = %v, want %v", filepath.Base(tc.path), got, tc.want)
		}
	}
}

// With binfmt_misc disabled, or not mounted, no handler recognizes a file: execve fails with ENOEXEC and execvp runs it with /bin/sh.
func TestExecveErrnoIgnoresADisabledBinfmtMisc(t *testing.T) {
	dir := t.TempDir()
	pe := writeProgram(t, dir, "tool.exe", "MZ windows program")
	fakeBinfmtMisc(t, "disabled", map[string]string{"WSLInterop": "enabled\ninterpreter /init\nflags: F\noffset 0\nmagic 4d5a\n"})
	if got := execveErrno(pe, dir, 0); got != syscall.ENOEXEC {
		t.Errorf("execveErrno with binfmt_misc disabled = %v, want ENOEXEC", got)
	}
	binfmtMiscDir = filepath.Join(dir, "not-mounted")
	if got := execveErrno(pe, dir, 0); got != syscall.ENOEXEC {
		t.Errorf("execveErrno without binfmt_misc = %v, want ENOEXEC", got)
	}
}

// On WSL the WSLInterop handler starts a Windows program directly, as libuv's execvp does for Pi. Skipped without an enabled handler for the MZ magic of a Windows program.
func TestWSLInteropStartsAWindowsProgram(t *testing.T) {
	if !slices.ContainsFunc(binfmtMiscHandlers(), func(h binfmtMiscHandler) bool { return h.offset == 0 && string(h.magic) == "MZ" }) {
		t.Skip("no enabled binfmt_misc handler for Windows programs")
	}
	cmd := exec.CommandContext(testbudget.Context(t), "cmd.exe", "/c", "echo x")
	SetProgram(cmd)
	if cmd.Err != nil {
		t.Skipf("cmd.exe is not on PATH: %v", cmd.Err)
	}
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "x" {
		t.Fatalf("cmd.exe /c echo x = %q, %v (path %q); want x", out, err, cmd.Path)
	}
}

// A line longer than bufio.Scanner's token limit must not turn a valid entry into a disabled one: the fields after it
// are still read.
func TestParseBinfmtMiscEntryReadsPastAVeryLongLine(t *testing.T) {
	data := "enabled\n" + "comment " + strings.Repeat("x", 100<<10) + "\ninterpreter /usr/bin/qemu\nflags: OF\nextension .foo\noffset 0\n"
	handler, ok := parseBinfmtMiscEntry([]byte(data))
	if !ok || handler.interpreter != "/usr/bin/qemu" {
		t.Fatalf("handler %+v ok %v, want the interpreter read", handler, ok)
	}
}
