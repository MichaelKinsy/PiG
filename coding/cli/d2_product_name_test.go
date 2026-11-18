package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// d2ProductNameTexts maps the fragments of Pi's messages that name the product to PiG's (D2, "Product name in messages",
// docs/parity/divergences/D2.md). A test that states Pi's text compares with PiG's through d2Text, so the one difference
// between the two is this table.
var d2ProductNameTexts = [][2]string{
	{"Session file is not a valid pi session: ", "Session file is not a valid pig session: "},
	{"Files may be in use by another pi session; ", "Files may be in use by another pig session; "},
}

// d2Text is Pi's text with each mapped fragment replaced by PiG's. A text that holds no mapped fragment is a mistake in the
// test: it panics.
func d2Text(piText string) string {
	pigText := piText
	for _, pair := range d2ProductNameTexts {
		pigText = strings.ReplaceAll(pigText, pair[0], pair[1])
	}
	if pigText == piText {
		panic("d2Text: no mapped product-name fragment in " + piText)
	}
	return pigText
}

// The help names PiG's variables; Pi's PI_ names keep working and are not listed.
func TestHelpNamesPigEnvironmentVariables(t *testing.T) {
	data, err := os.ReadFile("help_upstream.txt")
	if err != nil {
		t.Fatal(err)
	}
	help := string(data)
	for _, want := range []string{"(same as PIG_OFFLINE=1)\n", "\n  PIG_OFFLINE  ", "\n  PIG_SELF_UPDATE_COMMAND  ", "\n  PIG_TELEMETRY  "} {
		if !strings.Contains(help, want) {
			t.Errorf("help_upstream.txt lacks %q", want)
		}
	}
	for line := range strings.SplitSeq(help, "\n") {
		if strings.HasPrefix(line, "  PI_") || strings.Contains(line, "PI_OFFLINE") {
			t.Errorf("help_upstream.txt names a PI_ variable: %q", line)
		}
	}
}

// What `pig` exports as PI_TELEMETRY, for the Node extension runtime and every child process, is the resolved value. Owner decision
// 2026-11-17 (divergence record D2): when both names are set PIG_TELEMETRY wins, so a PIG_TELEMETRY=0 is never undone by a PI_TELEMETRY=1
// that is also in the environment (before the decision Pi's name won). PI_TELEMETRY alone stays as it is; with neither nothing is set.
// PIG_TELEMETRY itself is passed through untouched.
func TestExportTelemetryOverride(t *testing.T) {
	str := func(s string) *string { return &s }
	for _, tc := range []struct {
		name    string
		pig, pi *string
		want    string
		wantSet bool
	}{
		{"neither", nil, nil, "", false},
		{"PI alone", nil, str("1"), "1", true},
		{"PI alone off", nil, str("0"), "0", true},
		{"PIG alone", str("0"), nil, "0", true},
		{"PIG=0 PI=1", str("0"), str("1"), "0", true},
		{"PIG=1 PI=0", str("1"), str("0"), "1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for name, value := range map[string]*string{"PIG_TELEMETRY": tc.pig, "PI_TELEMETRY": tc.pi} {
				t.Setenv(name, "")
				if value == nil {
					_ = os.Unsetenv(name)
				} else {
					t.Setenv(name, *value)
				}
			}
			exportTelemetryOverride()
			got, ok := os.LookupEnv("PI_TELEMETRY")
			if ok != tc.wantSet || got != tc.want {
				t.Fatalf("PI_TELEMETRY = %q (set %v), want %q (set %v)", got, ok, tc.want, tc.wantSet)
			}
			if tc.pig != nil {
				if pig, _ := os.LookupEnv("PIG_TELEMETRY"); pig != *tc.pig {
					t.Fatalf("PIG_TELEMETRY = %q, want it passed through as %q", pig, *tc.pig)
				}
			}
		})
	}
}

// Pi's main.ts prints a failed `--export` as "Error: <message>" on stderr and exits 1, with nothing on stdout. The parity scenario
// cli-utils/28-export-cli-not-a-session compares the two binaries on the merged streams, because the product's name differs (D2); this
// test holds PiG's stream.
func TestExportOfAFileThatIsNotASessionAnswersOnStderr(t *testing.T) {
	dir := t.TempDir()
	garbage := filepath.Join(dir, "garbage.jsonl")
	if err := os.WriteFile(garbage, []byte("{\"type\":\"event\",\"data\":\"not a session\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runPigForModeTest(t, buildPigBinaryForSignalTest(t), nil, nil, "--export", garbage, filepath.Join(dir, "out.html"))
	want := d2Text("Error: Session file is not a valid pi session: ")
	if code != 1 || stdout != "" || !strings.HasPrefix(stderr, want) || !strings.HasSuffix(stderr, "garbage.jsonl\n") || strings.Count(stderr, "\n") != 1 {
		t.Fatalf("code=%d stdout=%q stderr=%q, want exit 1, no stdout and the one line %q<path> on stderr", code, stdout, stderr, want)
	}
}
