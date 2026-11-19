package codingagent

import (
	"os"
	"testing"
)

// telemetryPrecedenceCase is one environment (nil: unset) for PIG_TELEMETRY and PI_TELEMETRY with the install-telemetry setting.
type telemetryPrecedenceCase struct {
	name    string
	pig, pi *string
	setting bool
	want    bool
}

// setTelemetryEnv sets or unsets both names for one test and restores them afterwards.
func setTelemetryEnv(t *testing.T, pig, pi *string) {
	t.Helper()
	for name, value := range map[string]*string{"PIG_TELEMETRY": pig, "PI_TELEMETRY": pi} {
		t.Setenv(name, "")
		if value == nil {
			_ = os.Unsetenv(name)
		} else {
			t.Setenv(name, *value)
		}
	}
}

// The owner's decision 2026-11-17 (divergence record D2): PIG_TELEMETRY wins over PI_TELEMETRY when both are set, in either order of
// opposite values; each name alone decides; with neither the enableInstallTelemetry setting decides. Telemetry is a privacy control: a
// PIG_TELEMETRY=0 must never be overridden by a PI_TELEMETRY=1.
func telemetryPrecedenceCases() []telemetryPrecedenceCase {
	zero, one, yes, empty := new("0"), new("1"), new("yes"), new("")
	return []telemetryPrecedenceCase{
		{"PIG=0 PI=1 setting on: off", zero, one, true, false},
		{"PIG=1 PI=0 setting off: on", one, zero, false, true},
		{"PIG=0 PI=1 setting off: off", zero, one, false, false},
		{"PIG=1 PI=0 setting on: on", one, zero, true, true},
		{"PIG empty PI=1: off", empty, one, true, false},
		{"PIG=yes PI empty: on", yes, empty, false, true},
		{"PI=0 alone, setting on: off", nil, zero, true, false},
		{"PI=1 alone, setting off: on", nil, one, false, true},
		{"PIG=0 alone, setting on: off", zero, nil, true, false},
		{"PIG=1 alone, setting off: on", one, nil, false, true},
		{"neither, setting on: on", nil, nil, true, true},
		{"neither, setting off: off", nil, nil, false, false},
	}
}

func telemetrySettings(enabled bool) *SettingsManager {
	return &SettingsManager{merged: Settings{EnableInstallTelemetry: &enabled}}
}

func TestInstallTelemetryPigBeatsPiWhenBothAreSet(t *testing.T) {
	for _, tc := range telemetryPrecedenceCases() {
		t.Run(tc.name, func(t *testing.T) {
			setTelemetryEnv(t, tc.pig, tc.pi)
			if got := telemetrySettings(tc.setting).IsInstallTelemetryEnabled(); got != tc.want {
				t.Fatalf("IsInstallTelemetryEnabled = %v, want %v", got, tc.want)
			}
			value, ok := InstallTelemetryEnvOverride()
			switch {
			case tc.pig != nil:
				if !ok || value != *tc.pig {
					t.Fatalf("InstallTelemetryEnvOverride = %q, %v, want PIG_TELEMETRY's %q", value, ok, *tc.pig)
				}
			case tc.pi != nil:
				if !ok || value != *tc.pi {
					t.Fatalf("InstallTelemetryEnvOverride = %q, %v, want PI_TELEMETRY's %q", value, ok, *tc.pi)
				}
			case ok:
				t.Fatalf("InstallTelemetryEnvOverride = %q, true with neither name set", value)
			}
		})
	}
}

// The sender boundary: the install/update ping reaches a hermetic server exactly when the resolved decision is on.
func TestReportInstallTelemetryFollowsPigTelemetryOverPi(t *testing.T) {
	for _, tc := range telemetryPrecedenceCases() {
		t.Run(tc.name, func(t *testing.T) {
			setTelemetryEnv(t, tc.pig, tc.pi)
			t.Setenv("PI_OFFLINE", "")
			_ = os.Unsetenv("PI_OFFLINE")
			if !tc.want {
				t.Setenv("PIG_INSTALL_TELEMETRY_URL", "https://example.invalid/api/report-install")
				assertNoRequestSpawned(t, func() { reportInstallTelemetry(telemetrySettings(tc.setting), "0.2.0") })
				return
			}
			server, ch := newCapturingServer(t)
			t.Setenv("PIG_INSTALL_TELEMETRY_URL", server.URL+"/api/report-install")
			reportInstallTelemetry(telemetrySettings(tc.setting), "0.2.0")
			if req := awaitRequest(t, ch); req.URL.Query().Get("version") != "0.2.0" {
				t.Fatalf("request = %v, want the version ping", req.URL)
			}
		})
	}
}
