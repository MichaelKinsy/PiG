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
// follows it. The user's culture is set to de-DE for the test (and put back), and a fresh Node and a fresh process of this test
// binary (the culture is read when a process starts) print a number and a time with the default locale; they must be the same.
// It runs in the Windows CI job (Node of .node-version is on its PATH); there is no Windows host to run it on elsewhere.

const windowsLocaleHelper = "PIG_INTL_WINDOWS_LOCALE_HELPER"

func powershell(t *testing.T, script string) string {
	t.Helper()
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput()
	if err != nil {
		t.Fatalf("powershell %q: %v\n%s", script, err, out)
	}
	return strings.TrimSpace(string(out))
}

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
	if os.Getenv(windowsLocaleHelper) != "" {
		number, err := FormatNumber(nil, 1234567.891, nil)
		if err != nil {
			t.Fatal(err)
		}
		clock, err := formatClock(nil, time.Date(2026, time.June, 15, 14, 5, 7, 0, time.UTC), nil)
		if err != nil {
			t.Fatal(err)
		}
		os.Stdout.WriteString("RESULT " + number + "|" + clock + "\n")
		return
	}
	node := driftNode(t)
	original := powershell(t, "(Get-Culture).Name")
	t.Cleanup(func() { powershell(t, "Set-Culture "+original) })
	powershell(t, "Set-Culture de-DE")

	script := `const at = new Date(Date.UTC(2026, 5, 15, 14, 5, 7));
process.stdout.write("RESULT " + new Intl.NumberFormat().format(1234567.891) + "|" + new Intl.DateTimeFormat(undefined, { timeStyle: "short", timeZone: "UTC" }).format(at) + "\n");`
	cmd := exec.Command(node, "-e", script)
	cmd.Env = withoutPOSIXLocale()
	nodeOut, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	want := resultLine(t, string(nodeOut))
	if !strings.HasPrefix(want, "1.234.567,891|") {
		t.Fatalf("Node printed %q after Set-Culture de-DE: this runner's processes do not take the user's culture change, so the test proves nothing", want)
	}

	helper := exec.Command(os.Args[0], "-test.run=^TestWindowsDefaultLocaleIsTheUsersAsNodeTakesIt$")
	helper.Env = withoutPOSIXLocale(windowsLocaleHelper + "=1")
	pigOut, err := helper.Output()
	if err != nil {
		t.Fatalf("helper: %v\n%s", err, pigOut)
	}
	if got := resultLine(t, string(pigOut)); got != want {
		t.Fatalf("a format with no language is %q, Node's is %q", got, want)
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
