package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/core/experimental.ts

// Ports test/experimental.test.ts: PI_EXPERIMENTAL enables experimental features only when it is exactly "1". The PiG-only PIG_EXPERIMENTAL
// spelling is cleared so the cases isolate Pi's variable.
func TestAreExperimentalFeaturesEnabledMatchesPi(t *testing.T) {
	t.Setenv("PIG_EXPERIMENTAL", "")
	cases := []struct {
		name  string
		value string
		set   bool
		want  bool
	}{
		{"unset", "", false, false},
		{"empty", "", true, false},
		{"one", "1", true, true},
		{"zero", "0", true, false},
		{"true is not 1", "true", true, false},
		{"padded one is not 1", " 1 ", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.set {
				t.Setenv("PI_EXPERIMENTAL", c.value)
			} else {
				t.Setenv("PI_EXPERIMENTAL", "")
				_ = os.Unsetenv("PI_EXPERIMENTAL")
			}
			if got := AreExperimentalFeaturesEnabled(); got != c.want {
				t.Fatalf("PI_EXPERIMENTAL=%q (set=%v): %v, want %v", c.value, c.set, got, c.want)
			}
		})
	}
}

// areExperimentalFeaturesEnabled against pinned Pi over every PI_EXPERIMENTAL shape (unset, empty, 1, 0, true, padded, case, other numbers).
func TestAreExperimentalFeaturesEnabledMatchesPiOracle(t *testing.T) {
	t.Setenv("PIG_EXPERIMENTAL", "")
	values := []*string{nil}
	for _, v := range []string{"", "1", "0", "true", "TRUE", " 1 ", "1 ", "01", "2", "yes", "on", "-1", "1.0", "\t1"} {
		values = append(values, &v)
	}
	input, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/experimental_flag.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []bool
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for i, v := range values {
		if v == nil {
			_ = os.Unsetenv("PI_EXPERIMENTAL")
		} else {
			t.Setenv("PI_EXPERIMENTAL", *v)
		}
		if got := AreExperimentalFeaturesEnabled(); got != expected[i] {
			shown := "<unset>"
			if v != nil {
				shown = *v
			}
			t.Errorf("PI_EXPERIMENTAL=%q: PiG %v, Pi %v", shown, got, expected[i])
		}
	}
}
