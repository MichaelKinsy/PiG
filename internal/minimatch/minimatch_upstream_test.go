package minimatch

// Ports of the minimatch 11.0.3 tests that its commit range v10.2.6..v11.0.3 added or changed and that run on the options PiG ports (nocase, dot, nonegate, platform): test/end-check.ts, test/unc.ts, test/merge-rows.ts, test/match-rows.ts, test/windows-root-dotdot.ts (optimization level 1), test/multiple-non-adjacent-globstars.ts, test/split-cache.ts, test/cache.ts, test/escaping.js, test/patterns.js, test/tricky-negations.js, test/nocase-magic.js and test/the-sky-is-full-of-stars-at-least-100.ts. The inputs and expected results are the upstream ones; a row that sets an option PiG does not port (matchBase, partial, flipNegate, preserveMultipleSlashes, optimizationLevel 2, windowsPathsNoEscape, noext, braceExpandMax, maxGlobstarRecursion) is not ported. Rows are regenerated from the upstream files with the same inputs.

import (
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// upstreamRow is [f, p, match, options] of the upstream tables.
type upstreamRow struct {
	f, p        string
	want        bool
	nocase, dot bool
	platform    string
}

var endCheckRows = []upstreamRow{
	{"a/b/", "a/*", true, false, false, "posix"},
	{"a/b/", "a/**/*", true, false, false, "posix"},
	{"a/b/", "a/b/*", false, false, false, "posix"},
	{"a/b//", "a/*", true, false, false, "posix"},
	{"a/b/", "*/b/", true, false, false, "posix"},
	{"a/b", "*/b/", false, false, false, "posix"},
	{"a/c/", "{**/b,c}", false, false, false, "posix"},
	{"a/c/", "{**/c,b}", true, false, false, "posix"},
	{"", "", true, false, false, "posix"},
	{"", "*", false, false, false, "posix"},
	{"/", "/", true, false, false, "posix"},
	{"//", "/", true, false, false, "posix"},
	{"/", "*", false, false, false, "posix"},
	{"a/b/c", "a/**", true, false, false, "posix"},
	{"x/b/c", "a/**", false, false, false, "posix"},
	{"a.md", "!*.js", true, false, false, "posix"},
	{"a.js", "!*.js", false, false, false, "posix"},
	{"C:\\a\\b.js", "c:/a/*.js", true, false, false, "win32"},
	{"src/a.js", "src/**", true, false, false, "posix"},
	{"xsrc/a.js", "src/**", false, false, false, "posix"},
	{"a/srcx/b", "**/src/**", false, false, false, "posix"},
	{"a/src/b", "**/src/**", true, false, false, "posix"},
	{"a/.git/x", "**/.git/**", true, false, true, "posix"},
	{"a/git/x", "**/.git/**", false, false, true, "posix"},
	{"a/test/b/c.js", "**/test/**/*.js", true, false, false, "posix"},
	{"a/tests/b/c.js", "**/test/**/*.js", false, false, false, "posix"},
	{"b/x/c", "{a,b}/x/*", true, false, false, "posix"},
	{"c/x/c", "{a,b}/x/*", false, false, false, "posix"},
	{"lib/a.js", "!src/**", true, false, false, "posix"},
	{"src/a.js", "!src/**", false, false, false, "posix"},
	{"src/a", "SRC/**", true, true, false, "posix"},
	{"a/b/c.js", "**/*.js", true, false, false, "posix"},
	{"c.js", "**/*.js", true, false, false, "posix"},
	{"/a/c.js", "**/*.js", true, false, false, "posix"},
	{"a/.b/c.js", "**/*.js", false, false, false, "posix"},
	{"a/.b/c.js", "**/*.js", true, false, true, "posix"},
	{".a/c.js", "**/*.js", false, false, false, "posix"},
	{"a/./c.js", "**/*.js", false, false, true, "posix"},
	{"a/../c.js", "**/*.js", false, false, true, "posix"},
	{"../c.js", "**/*.js", false, false, true, "posix"},
	{"a/..b/c.js", "**/*.js", true, false, true, "posix"},
	{"a/.../c.js", "**/*.js", true, false, true, "posix"},
	{"a//b//c.js", "**/*.js", true, false, false, "posix"},
	{"a/c.js/", "**/*.js", true, false, false, "posix"},
	{"a/.b/c.js/", "**/*.js", false, false, false, "posix"},
	{".c.js", "**/*.js", false, false, false, "posix"},
	{".c.js", "**/*.js", true, false, true, "posix"},
	{"a/.c.js", "**/.*", true, false, false, "posix"},
	{"a/b/c", "**/c", true, false, false, "posix"},
	{"a/.b/c", "**/c", false, false, false, "posix"},
	{"a/b/c/", "**/c", true, false, false, "posix"},
	{"a/b/c.js", "!**/*.js", false, false, false, "posix"},
	{"a/.b/c.js", "!**/*.js", true, false, false, "posix"},
	{"a/b/c.js", "{**/*.js,x}", true, false, false, "posix"},
	{"a/.b/c.js", "{**/*.js,*/.b/*}", true, false, false, "posix"},
	{"C:\\src\\a.js", "c:/src/*.js", true, false, false, "win32"},
	{"c:\\src\\a.js", "C:/src/*.js", true, false, false, "win32"},
	{"D:\\src\\a.js", "c:/src/*.js", false, false, false, "win32"},
	{"C:\\lib\\a.js", "c:/src/*.js", false, false, false, "win32"},
	{"C:", "c:", true, false, false, "win32"},
	{"C:\\", "c:/", true, false, false, "win32"},
	{"//?/C:/src/a.js", "c:/src/*.js", true, false, false, "win32"},
	{"C:/src/a.js", "//?/c:/src/*.js", true, false, false, "win32"},
	{"\\\\?\\C:\\src\\a.js", "//?/c:/src/*.js", true, false, false, "win32"},
	{"//?/C:", "//?/c:", true, false, false, "win32"},
	{"//host/share/a.js", "//host/share/*.js", true, false, false, "win32"},
	{"\\\\host\\share\\a.js", "//host/share/*.js", true, false, false, "win32"},
	{"\\\\host\\other\\a.js", "//host/share/*.js", false, false, false, "win32"},
	{"a\\b\\c.js", "**/*.js", true, false, false, "win32"},
	{"a\\.b\\c.js", "**/*.js", false, false, false, "win32"},
	{"C:\\a\\c.js", "**/*.js", true, false, false, "win32"},
	{"//?/C:/a/c.js", "**/*.js", true, false, false, "win32"},
	{"a\\b\\c.md", "**/*.js", false, false, false, "win32"},
	{"a\\node_modules\\x", "**/node_modules/**", true, false, false, "win32"},
	{"a\\lib\\x", "**/node_modules/**", false, false, false, "win32"},
	{"a\\b\\c.js\\", "**/*.js", true, false, false, "win32"},
	{"C:\\src\\a.js", "c:/src/*.js", true, true, false, "win32"},
	{"C:\\SRC\\a.js", "c:/src/*.js", true, true, false, "win32"},
	{"a\\b/c.js", "**/*.js", true, false, false, "win32"},
	{"a/b\\c.js", "**/*.js", true, false, false, "win32"},
	{"a\\.b/c.js", "**/*.js", false, false, false, "win32"},
	{"a/.b\\c.js", "**/*.js", false, false, false, "win32"},
	{"a\\/.b\\/c.js", "**/*.js", false, false, false, "win32"},
	{"a/\\b\\/c.js\\/", "**/*.js", true, false, false, "win32"},
	{"node_modules/x", "**/node_modules/**", true, false, false, "posix"},
	{"a/node_modules/x", "**/node_modules/**", true, false, false, "posix"},
	{"a/b/node_modules/x/y", "**/node_modules/**", true, false, false, "posix"},
	{"a/node_modules", "**/node_modules/**", false, false, false, "posix"},
	{"a/node_modules/", "**/node_modules/**", true, false, false, "posix"},
	{"a/node_modulesx/y", "**/node_modules/**", false, false, false, "posix"},
	{"a/.b/node_modules/x", "**/node_modules/**", false, false, false, "posix"},
	{"a/.b/node_modules/x", "**/node_modules/**", true, false, true, "posix"},
	{"a/node_modules/.x", "**/node_modules/**", false, false, false, "posix"},
	{"a/node_modules/.x", "**/node_modules/**", true, false, true, "posix"},
	{"a/node_modules/x/..", "**/node_modules/**", false, false, true, "posix"},
	{"/a/node_modules/x", "**/node_modules/**", true, false, false, "posix"},
	{"a//node_modules//x", "**/node_modules/**", true, false, false, "posix"},
	{"a/.git/x", "**/.git/**", true, false, false, "posix"},
	{"a/.git/x", "**/.git/**", true, false, true, "posix"},
	{".git/x", "**/.git/**", true, false, true, "posix"},
	{"a/dist/x", "**/@(dist|build)/**", true, false, false, "posix"},
	{"a/build", "**/@(dist|build)/**", false, false, false, "posix"},
	{"a/x/build/y", "**/@(dist|build)/**", true, false, false, "posix"},
	{"a/distx/y", "**/@(dist|build)/**", false, false, false, "posix"},
	{"src/a", "src/**", true, false, false, "posix"},
	{"src/a/b", "src/**", true, false, false, "posix"},
	{"src", "src/**", false, false, false, "posix"},
	{"src/", "src/**", true, false, false, "posix"},
	{"a/src/b", "src/**", false, false, false, "posix"},
	{"src/.a", "src/**", false, false, false, "posix"},
	{"src/.a", "src/**", true, false, true, "posix"},
	{"src/a/..", "src/**", false, false, true, "posix"},
	{"lib/a", "@(src|lib)/**", true, false, false, "posix"},
	{"test/a", "@(src|lib)/**", false, false, false, "posix"},
	{"a\\node_modules\\x", "**/node_modules/**", true, false, false, "win32"},
	{"a\\.b\\node_modules\\x", "**/node_modules/**", false, false, false, "win32"},
	{"src\\a", "src/**", true, false, false, "win32"},
	{"a/node_modules/x", "!**/node_modules/**", false, false, false, "posix"},
	{"a/lib/x", "!**/node_modules/**", true, false, false, "posix"},
	{"a/b/x.js", "{**/b/**,c}", true, false, false, "posix"},
}

var uncRows = []upstreamRow{
	{"c:/x", "//?/c:/*", true, false, false, "win32"},
	{"//?/c:/x", "c:/*", true, false, false, "win32"},
	{"//?/c:/x", "//?/c:/*", true, false, false, "win32"},
	{"C:/x", "//?/c:/*", true, false, false, "win32"},
	{"//?/C:/x", "c:/*", true, false, false, "win32"},
	{"//?/C:/x", "//?/c:/*", true, false, false, "win32"},
	{"c:/x", "//?/C:/*", true, false, false, "win32"},
	{"//?/c:/x", "C:/*", true, false, false, "win32"},
	{"//?/c:/x", "//?/C:/*", true, false, false, "win32"},
	{"c:/x", "//?/C:/*", true, false, false, "win32"},
	{"c:/x", "C:/*", true, false, false, "win32"},
	{"C:/x", "//?/c:/*", true, false, false, "win32"},
	{"C:/x", "c:/*", true, false, false, "win32"},
	{"d:/x", "//?/c:/*", false, false, false, "win32"},
	{"//?/d:/x", "c:/*", false, false, false, "win32"},
	{"//?/d:/x", "//?/c:/*", false, false, false, "win32"},
	{"//x/c:/x", "//?/c:/*", false, false, false, "win32"},
	{"//x/y/z", "//x/y/*", true, false, false, "win32"},
	{"//HOST/SHARE/z", "//host/share/*", true, false, false, "win32"},
	{"//host/share/z", "//Host/Share/z", true, false, false, "win32"},
	{"//host/share", "//HOST/SHARE", true, false, false, "win32"},
	{"//host/share/a/b", "//HOST/SHARE/**", true, false, false, "win32"},
	{"//host/share/Z", "//HOST/SHARE/z", false, false, false, "win32"},
	{"//host/share/Z", "//HOST/SHARE/z", true, true, false, "win32"},
	{"//host/other/z", "//HOST/SHARE/*", false, false, false, "win32"},
	{"//other/share/z", "//HOST/SHARE/*", false, false, false, "win32"},
	{"//HOST/share/z", "//h*/share/*", false, false, false, "win32"},
	{"//HOST/share/z", "//h*/share/*", true, true, false, "win32"},
	{"//?/UNC/host/share/x", "//host/share/*", true, false, false, "win32"},
	{"//host/share/x", "//?/UNC/host/share/*", true, false, false, "win32"},
	{"//?/unc/Host/Share/x", "//?/UNC/HOST/share/x", true, false, false, "win32"},
	{"//?/UNC/host/share/a/b", "//HOST/SHARE/**", true, false, false, "win32"},
	{"//?/UNC/host/other/x", "//host/share/*", false, false, false, "win32"},
	{"//?/UNC/other/share/x", "//?/UNC/host/share/*", false, false, false, "win32"},
	{"//x/UNC/host/share/a", "//?/UNC/host/share/a", false, false, false, "win32"},
	{"//?/UNC/host/s1/x", "//h*/s?/x", true, false, false, "win32"},
	{"//host/s1/x", "//?/UNC/h*/s?/x", true, false, false, "win32"},
	{"//?/UNC/HOST/s1/x", "//h*/s?/x", false, false, false, "win32"},
	{"//?/UNC/host/x", "//?/UNC/host/share/../x", false, false, false, "win32"},
	{"//?/UNC/host/share/y", "//?/UNC/host/share/x/../y", true, false, false, "win32"},
}

// test/end-check.ts: each case is matched twice, as the upstream loop does, and must equal the full match.
func TestUpstreamEndCheck(t *testing.T) {
	for _, r := range endCheckRows {
		for range 2 {
			if got := Match(r.f, r.p, Options{NoCase: r.nocase, Dot: r.dot, Platform: r.platform}); got != r.want {
				t.Errorf("Match(%q, %q, nocase=%v dot=%v platform=%q) = %v, want %v", r.f, r.p, r.nocase, r.dot, r.platform, got, r.want)
			}
		}
	}
}

// test/unc.ts "UNC drive letter paths match normal paths": the file as given and with its slashes turned to backslashes.
func TestUpstreamUNC(t *testing.T) {
	for _, r := range uncRows {
		o := Options{NoCase: r.nocase, Dot: r.dot, Platform: r.platform}
		if got := Match(r.f, r.p, o); got != r.want {
			t.Errorf("Match(%q, %q) = %v, want %v", r.f, r.p, got, r.want)
		}
		if got := Match(strings.ReplaceAll(r.f, "/", `\`), r.p, o); got != r.want {
			t.Errorf("Match(%q, %q) = %v, want %v", strings.ReplaceAll(r.f, "/", `\`), r.p, got, r.want)
		}
	}
}

func portionKinds(row []portion) []string {
	out := make([]string, len(row))
	for i, p := range row {
		switch {
		case p.globstar:
			out[i] = "**"
		case p.m != nil:
			out[i] = "matcher:" + p.m.pattern
		default:
			out[i] = p.s
		}
	}
	return out
}

// test/unc.ts: long UNC roots are not magic, and their host and share are matchers only with magic.
func TestUpstreamLongUNCRoots(t *testing.T) {
	for _, o := range []Options{{Platform: "win32", NoCase: true}} {
		m, _ := New("//?/unc/host/share/*", o)
		if got, want := portionKinds(m.set[0]), []string{"", "", "?", "UNC", "host", "share", "matcher:*"}; !slices.Equal(got, want) {
			t.Errorf("set = %q, want %q", got, want)
		}
		for f, want := range map[string]bool{"//?/UNC/HOST/SHARE/x": true, "//HOST/share/x": true, "//x/UNC/host/share/x": false} {
			if got := m.Match(f); got != want {
				t.Errorf("Match(%q) = %v, want %v", f, got, want)
			}
		}
	}
	m, _ := New("//?/UNC/h*/share/*", Options{Platform: "win32", NoCase: true})
	if got, want := portionKinds(m.set[0]), []string{"", "", "?", "UNC", "matcher:h*", "matcher:share", "matcher:*"}; !slices.Equal(got, want) {
		t.Errorf("set = %q, want %q", got, want)
	}
	if !m.Match("//HOST/share/x") {
		t.Error(`Match("//HOST/share/x") = false, want true`)
	}
	share, _ := New("//host/share/*", Options{Platform: "win32"})
	if got, want := portionKinds(share.set[0]), []string{"", "", "host", "share", "matcher:*"}; !slices.Equal(got, want) {
		t.Errorf("set = %q, want %q", got, want)
	}
	unc, _ := New("//?/d:/*", Options{Platform: "win32"})
	if got, want := portionKinds(unc.set[0]), []string{"", "", "?", "d:", "matcher:*"}; !slices.Equal(got, want) {
		t.Errorf("set = %q, want %q", got, want)
	}
}

// test/unc.ts "matching a drive letter does not change the pattern".
func TestUpstreamDriveLetterMatchLeavesPattern(t *testing.T) {
	for _, pattern := range []string{"c:/x/**/y", "//?/c:/x/**/y"} {
		m, _ := New(pattern, Options{Platform: "win32"})
		before := make([][]string, len(m.set))
		for i, r := range m.set {
			before[i] = portionKinds(r)
		}
		for f, want := range map[string]bool{"C:/x/a/y": true, "c:/x/a/y": true, "//?/C:/x/y": true, "d:/x/a/y": false} {
			if got := m.Match(f); got != want {
				t.Errorf("%q: Match(%q) = %v, want %v", pattern, f, got, want)
			}
		}
		for i, r := range m.set {
			if !slices.Equal(portionKinds(r), before[i]) {
				t.Errorf("%q: Match changed row %d to %q", pattern, i, portionKinds(r))
			}
		}
	}
}

// test/merge-rows.ts: rows of brace expansion that differ in one matcher portion become one row with @(...).
func TestUpstreamMergeRows(t *testing.T) {
	cases := []struct {
		p     string
		parts [][]string
	}{
		{"**/*.{js,ts}", [][]string{{"**", "@(*.js|*.ts)"}}},
		{"{a,b}*/{c,d}*", [][]string{{"@(a*|b*)", "@(c*|d*)"}}},
		{"x/{*.a,*.b}/{*.c,d}", [][]string{{"x", "@(*.a|*.b)", "*.c"}, {"x", "@(*.a|*.b)", "d"}}},
		// strings stay strings, so they have no magic
		{"{a,b}/x", [][]string{{"a", "x"}, {"b", "x"}}},
		// portions that would change meaning in @(...)
		{"{x|,y}*", [][]string{{"x|*"}, {"y*"}}},
		{"{@(a,b*}", [][]string{{"@(a"}, {"b*"}}},
		{"{*(a)b),c*}", [][]string{{"*(a)b)"}, {"c*"}}},
		{"{?(),*a}", [][]string{{"?()"}, {"*a"}}},
		{"{[[:alpha:]],*a}", [][]string{{"[[:alpha:]]"}, {"*a"}}},
		{"{a,b}*\\", [][]string{{"a*\\"}, {"b*\\"}}},
	}
	for _, c := range cases {
		m, _ := New(c.p, Options{})
		if !reflect.DeepEqual(m.globParts, c.parts) {
			t.Errorf("%q: globParts = %q, want %q", c.p, m.globParts, c.parts)
		}
	}
	m, _ := New("{a,b}*/{c,d}*", Options{})
	if len(m.set) != len(m.globParts) || !slices.Equal(portionKinds(m.set[0]), []string{"matcher:@(a*|b*)", "matcher:@(c*|d*)"}) {
		t.Errorf("set = %v for globParts %q", m.set, m.globParts)
	}
	re := regexp.MustCompile(`^(.*/)?[^.][^/]*\.[jt]s$`)
	for _, f := range []string{"x.js", "a/x.ts", "a/x.md", ".x.js", "a/.x.ts"} {
		if got := Match(f, "**/*.{js,ts}", Options{}); got != re.MatchString(f) {
			t.Errorf("Match(%q) = %v, want %v", f, got, re.MatchString(f))
		}
	}
	if !Match(".x.js", "**/*.{js,ts}", Options{Dot: true}) {
		t.Error("dot: .x.js does not match")
	}
}

// test/match-rows.ts: a row of the set is matched against the split path, and a drive letter on Windows.
func TestUpstreamMatchRows(t *testing.T) {
	for _, r := range []upstreamRow{
		{"a/b/c", "a/*/c", true, false, false, ""},
		{"a/b/d", "a/*/c", false, false, false, ""},
		{"a/b", "a/*/c", false, false, false, ""},
		{"a/x/y/c.js", "a/**/c.js", true, false, false, ""},
		{"a/.x/c.js", "a/**/c.js", false, false, false, ""},
	} {
		if got := Match(r.f, r.p, Options{}); got != r.want {
			t.Errorf("Match(%q, %q) = %v, want %v", r.f, r.p, got, r.want)
		}
	}
	m, _ := New("c:/x", Options{Platform: "win32"})
	if !m.Match("C:/x") || m.Match("d:/x") {
		t.Error("c:/x on win32 must match C:/x and not d:/x")
	}
	// the row is not changed to the case of the path
	if got := portionKinds(m.set[0]); !slices.Equal(got, []string{"c:", "x"}) {
		t.Errorf("set = %q, want [c: x]", got)
	}
}

// test/windows-root-dotdot.ts at optimization level 1 (the PiG port runs level 1): .. cannot remove the root of a Windows pattern or path.
func TestUpstreamWindowsRootDotDot(t *testing.T) {
	cases := []struct {
		p     string
		parts []string
	}{
		{"C:/foo/../..", []string{"C:", ".."}},
		{"C:/../x", []string{"C:", "..", "x"}},
		{"C:/a/../../x", []string{"C:", "..", "x"}},
		{"C:/a/b/../../c", []string{"C:", "c"}},
		{"//srv/share/../x", []string{"", "", "srv", "share", "..", "x"}},
		{"//srv/share/a/../../x", []string{"", "", "srv", "share", "..", "x"}},
		{"//srv/..", []string{"", "", "srv", ".."}},
		{"//?/C:/a/../../x", []string{"", "", "?", "C:", "..", "x"}},
		{"//?/UNC/srv/share/a/../../x", []string{"", "", "?", "UNC", "srv", "share", "..", "x"}},
		{"//srv/*/a/../../x", []string{"", "", "srv", "*", "..", "x"}},
	}
	for _, c := range cases {
		m, _ := New(c.p, Options{Platform: "win32"})
		if !reflect.DeepEqual(m.globParts, [][]string{c.parts}) {
			t.Errorf("%q: globParts = %q, want %q", c.p, m.globParts, c.parts)
		}
	}
	// the same as the posix root
	posix, _ := New("/a/../../x", Options{Platform: "linux"})
	if !reflect.DeepEqual(posix.globParts, [][]string{{"", "..", "x"}}) {
		t.Errorf("posix globParts = %q", posix.globParts)
	}
	for _, r := range []upstreamRow{
		{"/", "C:/foo/../..", false, false, false, "win32"},
		{"C:/x", "C:/../x", false, false, false, "win32"},
		{"/x", "C:/a/../../x", false, false, false, "win32"},
		{"x", "C:/a/../../x", false, false, false, "win32"},
		{"//srv/x", "//srv/share/../x", false, false, false, "win32"},
		{"C:/c", "C:/a/b/../../c", true, false, false, "win32"},
		{"//srv/share/y", "//srv/share/x/../y", true, false, false, "win32"},
	} {
		if got := Match(r.f, r.p, Options{Platform: r.platform}); got != r.want {
			t.Errorf("Match(%q, %q) = %v, want %v", r.f, r.p, got, r.want)
		}
	}
}

// test/multiple-non-adjacent-globstars.ts, without the options PiG does not port: the globstar walks stay fast, and a body section is tried once per place.
func TestUpstreamNonAdjacentGlobstars(t *testing.T) {
	fast := func(name string, f func()) {
		start := time.Now()
		f()
		if d := time.Since(start); d > time.Second {
			t.Errorf("%s took %v, want under 1s", name, d)
		}
	}
	pattern := strings.Repeat("**/a/", 50) + "b/**"
	patha := strings.Repeat("a/", 100) + "a"
	pathb := strings.Repeat("a/", 100) + "b/c/d/.e/a/b"
	fast("GHSA-7r86-cg39-jmmj", func() {
		if Match(patha, pattern, Options{}) || !Match(pathb, pattern, Options{Dot: true}) || Match(pathb, pattern, Options{}) {
			t.Error("GHSA-7r86-cg39-jmmj: wrong result")
		}
	})
	alphabet := strings.Repeat("abcdefghijklmnopqrstuvwxyz", 5)
	letters := strings.Split(alphabet, "")
	exclude := func(c string) []string {
		return slices.DeleteFunc(slices.Clone(letters), func(x string) bool { return x == c })
	}
	var all []string
	for _, c := range letters {
		all = append(all, exclude(c)...)
	}
	path := strings.Join(all, "/") + "/" + strings.Join(append(exclude("a"), "a"), "/")
	fast("alphabetical", func() {
		if !Match(path, "**/"+strings.Join(letters, "/**/")+"/**", Options{}) {
			t.Error("alphabetical: want a match")
		}
	})
	for _, r := range []upstreamRow{
		{".x/.y/.z/", ".x/**/*/*/**", true, false, true, ""},
		{".x/.y/.z", ".x/**/*/*/**", false, false, true, ""},
		{".x/.y/.z/", ".x/**/**/*", true, false, true, ""},
		{".x/", ".x/**/**/*", false, false, true, ""},
		// body sections that differ in length (minimatch#329) at level 1
		{"a/b/c/d/e", "**/a/b/c/**/d/**", true, false, false, ""},
		{"x/a/b/c/d/e", "**/a/b/c/**/d/**", true, false, false, ""},
		{"a/b/c/x/d/e", "**/a/b/c/**/d/**", true, false, false, ""},
		{"a/b/c/e", "**/a/b/c/**/d/**", false, false, false, ""},
		{"a/b/c/d", "**/a/b/c/**/d/**", false, false, false, ""},
		{"a/b/c/d/e", "**/d/**/a/b/c/**", false, false, false, ""},
		{"d/x/a/b/c/e", "**/d/**/a/b/c/**", true, false, false, ""},
		// the tail is not walked by the last **
		{".x/.x", "**/.x/**/.x", true, false, false, ""},
		{"a/.x/b/.x", "**/.x/**/.x", true, false, false, ""},
		{"a/.x/.y/.x", "**/.x/**/.x", false, false, false, ""},
	} {
		if got := Match(r.f, r.p, Options{Dot: r.dot}); got != r.want {
			t.Errorf("Match(%q, %q, dot=%v) = %v, want %v", r.f, r.p, r.dot, got, r.want)
		}
	}
}

// test/split-cache.ts: windows and posix split the same path differently.
func TestUpstreamSplitCache(t *testing.T) {
	win, _ := New("a/b", Options{Platform: "win32"})
	posix, _ := New("a/b", Options{Platform: "linux"})
	for i, want := range []bool{true, false, true} {
		if got := win.Match(`a\b`); i != 1 && got != want {
			t.Errorf("win32 Match(a\\b) = %v, want %v", got, want)
		}
		if posix.Match(`a\b`) {
			t.Error(`posix Match(a\b) = true, want false`)
		}
	}
}

// test/escaping.js "escapes in *.ext are not compared literally", test/cache.ts, test/the-sky-is-full-of-stars-at-least-100.ts and test/tricky-negations.js.
func TestUpstreamChangedCases(t *testing.T) {
	for _, r := range []upstreamRow{
		{"a.js", `*.j\s`, true, false, false, ""},
		{`a.j\s`, `*.j\s`, false, false, false, ""},
		{"a.JS", `*.j\s`, true, true, false, ""},
		{".x", "*", false, false, false, ""},
		{".x", "*", true, false, true, ""},
		{"X", "x", true, true, false, ""},
		{"X", "x", false, false, false, ""},
		{"foojs.js", "*(*.json|!(*.js))", true, false, false, ""},
		{"foojs.js", "+(*.json|!(*.js))", true, false, false, ""},
		{"foojs.js", "@(*.json|!(*.js))", false, false, false, ""},
		{"foojs.js", "?(*.json|!(*.js))", false, false, false, ""},
	} {
		if got := Match(r.f, r.p, Options{NoCase: r.nocase, Dot: r.dot}); got != r.want {
			t.Errorf("Match(%q, %q, nocase=%v dot=%v) = %v, want %v", r.f, r.p, r.nocase, r.dot, got, r.want)
		}
	}
	me, _ := New("a/*/b", Options{})
	sky, _ := New("a/"+strings.Repeat("*", 100)+"/b", Options{})
	for _, f := range []string{"a/x/b", "a/.x/b", "a//b", "a/x/y/b", "a/x/c"} {
		if sky.Match(f) != me.Match(f) {
			t.Errorf("a hundred stars and one differ for %q", f)
		}
	}
	// test/nocase-magic.js: a portion with magic is a matcher with nocase, the rest strings.
	m, _ := New("../x*", Options{NoCase: true})
	if got := portionKinds(m.set[0]); !slices.Equal(got, []string{"..", "matcher:x*"}) {
		t.Errorf("set = %q", got)
	}
}

// test/patterns.js: the cases whose expected files changed in 11.0.3, with nonegate where upstream sets it.
func TestUpstreamChangedPatterns(t *testing.T) {
	list := func(files []string, pattern string, o Options) []string {
		var out []string
		for _, f := range files {
			if Match(f, pattern, o) {
				out = append(out, f)
			}
		}
		return out
	}
	// an unclosed extglob start is a literal; Bash 5 does not match this file
	files := []string{`+(a|b\|c\\|d\\|e\\\\|f\\\\|g`, "a", `b\c`}
	if got := list(files, `+(a|*\|c\\|d\\\|e\\\\|f\\\\\|g`, Options{}); len(got) != 0 {
		t.Errorf("unclosed +( matched %q, want none", got)
	}
	// an unclosed *( is a literal *(, as in Bash 5
	braced := []string{"a", "(b|c", "*(b|c", "d)"}
	if got := list(braced, "{a,*(b|c,d)}", Options{}); !slices.Equal(got, []string{"a", "*(b|c", "d)"}) {
		t.Errorf("{a,*(b|c,d)} matched %q", got)
	}
	// !(.a|js) can match the empty string, then @(.*) matches the dotfile
	dots := []string{".a", ".a.js", ".js", "a.js"}
	if got := list(dots, "!(.a|js)@(.*)", Options{NoNegate: true}); !slices.Equal(got, dots) {
		t.Errorf("!(.a|js)@(.*) matched %q, want all of %q", got, dots)
	}
}
