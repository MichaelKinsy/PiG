package nodepath

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Random paths built from the pieces that make Node's path code branch (separators of both kinds, dots, drive letters, UNC prefixes, device paths, trailing separators), against path.posix and path.win32.
func TestRandomPathsMatchNode(t *testing.T) {
	pieces := []string{"a", "b", "ab", ".", "..", "...", "/", "/", "\\", "\\", ":", "C:", "c:", "D:", "//", "\\\\", "?", "x.y", ".hidden", "a.", "é", " ", "server", "share", "UNC", "NUL", "con", "COM1", "lpt\u00b2", "COM10", "\U0001F600"}
	rng := rand.New(rand.NewSource(61))
	random := func() string {
		var b strings.Builder
		for range rng.Intn(9) {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		return b.String()
	}
	type probe struct {
		A string `json:"a"`
		B string `json:"b"`
	}
	type result struct {
		PosixNormalize, PosixJoin, PosixDirname, PosixBasename string
		Win32Normalize, Win32Join, Win32Dirname, Win32Basename string
		PosixIsAbsolute, Win32IsAbsolute                       bool
	}
	probes := make([]probe, 30000)
	for i := range probes {
		probes[i] = probe{A: random(), B: random()}
	}
	var want []result
	pioracle.Run(t, `const path = (await import("node:path")).default;
emit(input.map(({a, b}) => ({
  PosixNormalize: path.posix.normalize(a), PosixJoin: path.posix.join(a, b), PosixDirname: path.posix.dirname(a), PosixBasename: path.posix.basename(a),
  Win32Normalize: path.win32.normalize(a), Win32Join: path.win32.join(a, b), Win32Dirname: path.win32.dirname(a), Win32Basename: path.win32.basename(a),
  PosixIsAbsolute: path.posix.isAbsolute(a), Win32IsAbsolute: path.win32.isAbsolute(a),
})));`, probes, &want)
	bad := 0
	report := func(name, a, b, got, want string) {
		if got != want {
			if bad++; bad <= 15 {
				t.Errorf("%s(%q, %q) = %q, Node %q", name, a, b, got, want)
			}
		}
	}
	for i, p := range probes {
		w := want[i]
		report("PosixNormalize", p.A, "", PosixNormalize(p.A), w.PosixNormalize)
		report("PosixJoin", p.A, p.B, PosixJoin(p.A, p.B), w.PosixJoin)
		report("PosixDirname", p.A, "", PosixDirname(p.A), w.PosixDirname)
		report("PosixBasename", p.A, "", PosixBasename(p.A), w.PosixBasename)
		report("Win32Normalize", p.A, "", Win32Normalize(p.A), w.Win32Normalize)
		report("Win32Join", p.A, p.B, Win32Join(p.A, p.B), w.Win32Join)
		report("Win32Dirname", p.A, "", Win32Dirname(p.A), w.Win32Dirname)
		report("Win32Basename", p.A, "", Win32Basename(p.A), w.Win32Basename)
		if PosixIsAbsolute(p.A) != w.PosixIsAbsolute {
			t.Errorf("PosixIsAbsolute(%q) differs", p.A)
		}
		if Win32IsAbsolute(p.A) != w.Win32IsAbsolute {
			t.Errorf("Win32IsAbsolute(%q) differs", p.A)
		}
	}
	if bad > 0 {
		t.Errorf("%d differences", bad)
	}
}
