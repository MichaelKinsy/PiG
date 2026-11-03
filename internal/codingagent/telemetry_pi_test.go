package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/core/telemetry.ts

// telemetryGateCase is one PI_TELEMETRY value (null: unset) with the install-telemetry setting.
type telemetryGateCase struct {
	Env     *string `json:"env"`
	Setting bool    `json:"setting"`
}

// isInstallTelemetryEnabled of the installed Pi against installTelemetryAllowed, the gate reportInstallTelemetry applies after the PI_OFFLINE check
// (interactive-mode.ts reportInstallTelemetry), and SettingsManager.IsInstallTelemetryEnabled: every PI_TELEMETRY spelling (unset, empty, 1, true,
// yes in each case, 0, false, no, padded, other) against the setting both ways. PI_OFFLINE is left unset, so the gate is Pi's alone.
func TestInstallTelemetryGateMatchesPiOracle(t *testing.T) {
	t.Setenv("PI_OFFLINE", "")
	var cases []telemetryGateCase
	for _, setting := range []bool{true, false} {
		cases = append(cases, telemetryGateCase{Setting: setting})
		for _, v := range []string{"", "1", "0", "true", "TRUE", "True", "yes", "YES", "Yes", "no", "false", "on", "off", " 1", "1 ", "01", "2", "-1", "maybe"} {
			cases = append(cases, telemetryGateCase{Env: &v, Setting: setting})
		}
	}
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/install_telemetry_gate.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []bool
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		if c.Env == nil {
			_ = os.Unsetenv("PI_TELEMETRY")
		} else {
			t.Setenv("PI_TELEMETRY", *c.Env)
		}
		setting := c.Setting
		sm := &SettingsManager{merged: Settings{EnableInstallTelemetry: &setting}}
		shown := "<unset>"
		if c.Env != nil {
			shown = *c.Env
		}
		if got := sm.IsInstallTelemetryEnabled(); got != want[i] {
			t.Errorf("IsInstallTelemetryEnabled with PI_TELEMETRY=%q, setting=%v: %v, Pi %v", shown, c.Setting, got, want[i])
		}
		if got := installTelemetryAllowed(sm); got != want[i] {
			t.Errorf("installTelemetryAllowed with PI_TELEMETRY=%q, setting=%v: %v, Pi %v", shown, c.Setting, got, want[i])
		}
	}
}
