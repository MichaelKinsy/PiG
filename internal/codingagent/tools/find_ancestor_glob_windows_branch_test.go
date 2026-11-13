package tools

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// The Windows branch of Pi 282b01c80 (#12116), run with the platform injected: the matcher of a relative path pattern matches the path relative
// to the search path, so a folder above the search path with the pattern's first name does not match (12066-find-ancestor-glob.test.ts's
// project in ...\src\app), and the filter keeps the absolute lines fd printed.
func TestFindWindowsPathPatternDoesNotMatchAboveTheSearchPath(t *testing.T) {
	const search = `C:\tmp\src\app`
	lines := []string{`C:\tmp\src\app\src\a.ts`, `C:\tmp\src\app\lib\b.ts`, `C:\tmp\src\app\test\c.ts`, `C:\tmp\src\app\pkg\src\d.ts`}
	for _, tc := range []struct {
		pattern string
		want    []string
	}{
		{"src/**/*.ts", []string{lines[0]}},
		{"app/**/*.ts", nil},
		{"src/*.ts", []string{lines[0]}},
		{"./src/*.ts", []string{lines[0]}},
		{`src\*.ts`, []string{lines[0]}},
		{"pkg/src/*.ts", []string{lines[3]}},
		{"**/src/*.ts", []string{lines[0], lines[3]}},
		{"**/src/**/*.ts", []string{lines[0], lines[3]}},
		{"C:/tmp/src/app/lib/*.ts", []string{lines[1]}},
	} {
		inv, err := newFindFdInvocation(tc.pattern, search, findDefaultLimit, true, true)
		if err != nil || inv.pathMatcher == nil {
			t.Fatalf("%q: %+v, %v", tc.pattern, inv, err)
		}
		filter := &findPathFilter{matcher: inv.pathMatcher, limit: findDefaultLimit, stop: func() {}}
		if inv.matchRelative {
			filter.relativize = func(line string) string { return relativizeFindResultPathFor(line, search, true, nodepath.Process()) }
		}
		for _, line := range lines {
			_, _ = filter.Write([]byte(line + "\r\n"))
		}
		filter.finish()
		if got := outputLines(filter.kept.String()); !slices.Equal(got, tc.want) {
			t.Errorf("%q kept %q, want %q", tc.pattern, got, tc.want)
		}
	}
}
