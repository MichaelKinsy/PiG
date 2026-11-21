//go:build windows

package surfaces

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The default locale of Pi's `Intl` on Windows is the user's (ICU's `uprv_getDefaultLocaleID`, Windows branch, putil.cpp:1796:
// `GetLocaleInfoEx(LOCALE_NAME_USER_DEFAULT, LOCALE_SNAME)`; no environment variable is read), so a format with no language
// follows it. This test never mutates the machine's or user's Windows culture (no `Set-Culture` or registry write): it checks
// read-only that PiG's platform locale and nil-language formats match a live Node on the current Windows account (ignoring any
// POSIX locale environment variables), and exercises non-English / sort-order / fallback Windows locale names through the
// readWindowsLocaleName seam against the same Node.

// withoutPOSIXLocale is the environment without the variables PiG's other systems read, which a Windows user does not have.
func withoutPOSIXLocale(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "LC_ALL" || name == "LC_MESSAGES" || name == "LANG" {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

func TestWindowsDefaultLocaleIsTheUsersAsNodeTakesIt(t *testing.T) {
	node := driftNode(t)
	// Confirm read-only that Windows ignores POSIX locale env vars and agrees with Node on this account's real culture.
	t.Setenv("LC_ALL", "fr_FR.UTF-8")
	t.Setenv("LC_MESSAGES", "fr_FR.UTF-8")
	t.Setenv("LANG", "fr_FR.UTF-8")

	const script = `const at = new Date(Date.UTC(2026, 5, 15, 14, 5, 7));
const loc = new Intl.DateTimeFormat().resolvedOptions().locale;
const num = new Intl.NumberFormat().format(1234567.891);
const clock = new Intl.DateTimeFormat(undefined, { timeStyle: "short", timeZone: "UTC" }).format(at);
process.stdout.write("RESULT " + loc + "|" + num + "|" + clock + "\n");`
	cmd := exec.Command(node, "-e", script)
	cmd.Env = withoutPOSIXLocale()
	nodeOut, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	want := resultLine(t, string(nodeOut))

	at := time.Date(2026, time.June, 15, 14, 5, 7, 0, time.UTC)
	number, err := FormatNumber(nil, 1234567.891, nil)
	if err != nil {
		t.Fatal(err)
	}
	clock, err := formatClock(nil, at, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Resolve PlatformLocale through Node's canonicalization form by comparing both the locale name and formatted output.
	got := PlatformLocale() + "|" + number + "|" + clock
	t.Logf("read-only Windows user locale comparison: pig=%q node=%q", got, want)
	wantParts := strings.SplitN(want, "|", 2)
	gotParts := strings.SplitN(got, "|", 2)
	if gotParts[1] != wantParts[1] {
		t.Fatalf("a format with no language is %q (PlatformLocale=%q), Node's is %q (locale=%q)", gotParts[1], gotParts[0], wantParts[1], wantParts[0])
	}
	if !strings.EqualFold(gotParts[0], wantParts[0]) {
		t.Fatalf("PlatformLocale() = %q, Node resolvedOptions().locale = %q", gotParts[0], wantParts[0])
	}

	// Seam check: exercise userLocale() across Windows LOCALE_SNAME shapes (including non-English, script, sort suffix, and
	// failure) without changing the Windows account's culture, comparing the resulting nil-language formats against Node.
	saved := readWindowsLocaleName
	t.Cleanup(func() { readWindowsLocaleName = saved })
	cases := []struct {
		sname   string
		wantTag string
		nodeTag string
	}{
		{"de-DE", "de-DE", "de-DE"},
		{"de-CH", "de-CH", "de-CH"},
		{"zh-Hant-TW", "zh-Hant-TW", "zh-Hant-TW"},
		{"de-DE_phoneb", "de-DE-phoneb", "de-DE"},
		{"", "en-US", "en-US"},
	}
	for _, tc := range cases {
		t.Run(tc.sname, func(t *testing.T) {
			readWindowsLocaleName = func() string { return tc.sname }
			if gotTag := PlatformLocale(); gotTag != tc.wantTag {
				t.Fatalf("PlatformLocale() for LOCALE_SNAME %q = %q, want %q", tc.sname, gotTag, tc.wantTag)
			}
			gotNum, err := FormatNumber(nil, 1234567.891, nil)
			if err != nil {
				t.Fatalf("FormatNumber(nil) for %q: %v", tc.sname, err)
			}
			gotClock, err := formatClock(nil, at, nil)
			if err != nil {
				t.Fatalf("formatClock(nil) for %q: %v", tc.sname, err)
			}
			nodeScript := `const at = new Date(Date.UTC(2026, 5, 15, 14, 5, 7));
const tag = process.argv[1];
process.stdout.write("RESULT " + new Intl.NumberFormat(tag).format(1234567.891) + "|" + new Intl.DateTimeFormat(tag, { timeStyle: "short", timeZone: "UTC" }).format(at) + "\n");`
			c := exec.Command(node, "-e", nodeScript, tc.nodeTag)
			c.Env = withoutPOSIXLocale()
			out, err := c.Output()
			if err != nil {
				t.Fatalf("node(%s): %v", tc.nodeTag, err)
			}
			wantFmt := resultLine(t, string(out))
			if gotFmt := gotNum + "|" + gotClock; gotFmt != wantFmt {
				t.Fatalf("LOCALE_SNAME %q formatted %q, Node(%s) formatted %q", tc.sname, gotFmt, tc.nodeTag, wantFmt)
			}
		})
	}
}

// resultLine is the text after RESULT in the output's one line that carries it.
func resultLine(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if text, ok := strings.CutPrefix(strings.TrimSpace(line), "RESULT "); ok {
			return text
		}
	}
	t.Fatalf("no RESULT line in %q", out)
	return ""
}
