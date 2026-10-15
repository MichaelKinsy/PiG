package nodepath

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pathRow is three strings built from separators, drive letters, UNC and device roots, Windows reserved device names ("CON:", "COM1:",
// "COM\u00b9:", a name with a trailing letter, which Node's colon-less slice(0, -1) also counts) and one inserted character.
type pathRow struct{ A, B, C string }

func randomPathRows(count int) []pathRow {
	random := rand.New(rand.NewSource(9))
	pieces := []string{
		"", ".", "..", "/", "\\", "//", "\\\\", "a", "b", "a/b", "a\\b", "C:", "C:\\", "C:/", "C:a", "c:\\x", "D:..", "//server/share", "\\\\server\\share",
		"\\\\?\\C:\\x", "\\\\.\\pipe\\x", "\\\\?\\UNC\\s\\h", "a:b", ":", "...", "é", " ", "a.txt", ".bashrc", "a.", "..a", "x.tar.gz", "\\\\?\\", "\\\\.\\", "//?/c:/",
		"C:\\a\\..", "\x00", "CON\u00e9", "CON\U0001F600", "CON:", "com1:", "NUL:x", "LPT\u00b9:", "\u00b2", "\\\\?\\COM1:", "\\\\.\\con:", "CON", "CONx", "COM\u00b9:a",
		"prn", "AUX:", "\\\\?\\a\\b", "\\\\.\\NUL:x",
	}
	build := func() string {
		var b strings.Builder
		for range random.Intn(4) + 1 {
			b.WriteString(pieces[random.Intn(len(pieces))])
		}
		text := []rune(b.String())
		if random.Intn(4) == 0 && len(text) > 0 {
			at := random.Intn(len(text))
			return string(text[:at]) + string("/\\:. aZ"[random.Intn(7)]) + string(text[at:])
		}
		return string(text)
	}
	rows := make([]pathRow, count)
	for i := range rows {
		rows[i] = pathRow{build(), build(), build()}
	}
	return rows
}

// path.win32 and path.posix against the Go ports for generated inputs: normalize, join of three, basename, dirname, isAbsolute,
// toNamespacedPath, resolve and relative. The working directory is Node's own.
func TestPathFlavorsMatchNodeOnRandomInputs(t *testing.T) {
	rows := randomPathRows(3000)
	var want []map[string]string
	pioracle.Run(t, `
const path = (await import("node:path")).default;
const results = [];
for (const flavor of ["posix", "win32"]) {
  const p = path[flavor];
  for (const r of input) {
    const o = {};
    o.norm = p.normalize(r.A);
    o.join = p.join(r.A, r.B, r.C);
    o.base = p.basename(r.A);
    o.dir = p.dirname(r.A);
    o.abs = String(p.isAbsolute(r.A));
    o.ns = p.toNamespacedPath(r.A);
    o.res = p.resolve(r.A, r.B);
    o.rel = p.relative(r.A, r.B);
    results.push(o);
  }
}
emit(results);`, rows, &want)
	cwd, err := Cwd() // Node resolves against process.cwd(), the physical directory; go test's $PWD may name it through a link (/tmp on macOS)
	if err != nil {
		t.Fatal(err)
	}
	env := Env{Cwd: cwd}
	bad := 0
	check := func(flavor, what, got, want string, args ...string) {
		if got != want {
			if bad++; bad <= 30 {
				t.Errorf("%s %s%q = %q, Node %q", flavor, what, args, got, want)
			}
		}
	}
	for i, r := range rows {
		posix, win := want[i], want[len(rows)+i]
		check("posix", "normalize", PosixNormalize(r.A), posix["norm"], r.A)
		check("posix", "join", PosixJoin(r.A, r.B, r.C), posix["join"], r.A, r.B, r.C)
		check("posix", "basename", PosixBasename(r.A), posix["base"], r.A)
		check("posix", "dirname", PosixDirname(r.A), posix["dir"], r.A)
		check("posix", "isAbsolute", strconv.FormatBool(PosixIsAbsolute(r.A)), posix["abs"], r.A)
		resolved, _ := PosixResolve(env, r.A, r.B)
		check("posix", "resolve", resolved, posix["res"], r.A, r.B)
		relative, _ := PosixRelative(env, r.A, r.B)
		check("posix", "relative", relative, posix["rel"], r.A, r.B)

		check("win32", "normalize", Win32Normalize(r.A), win["norm"], r.A)
		check("win32", "join", Win32Join(r.A, r.B, r.C), win["join"], r.A, r.B, r.C)
		check("win32", "basename", Win32Basename(r.A), win["base"], r.A)
		check("win32", "dirname", Win32Dirname(r.A), win["dir"], r.A)
		check("win32", "isAbsolute", strconv.FormatBool(Win32IsAbsolute(r.A)), win["abs"], r.A)
		namespaced, _ := Win32ToNamespacedPath(env, r.A)
		check("win32", "toNamespacedPath", namespaced, win["ns"], r.A)
		resolved, _ = Win32Resolve(env, r.A, r.B)
		check("win32", "resolve", resolved, win["res"], r.A, r.B)
		relative, _ = Win32Relative(env, r.A, r.B)
		check("win32", "relative", relative, win["rel"], r.A, r.B)
	}
}
