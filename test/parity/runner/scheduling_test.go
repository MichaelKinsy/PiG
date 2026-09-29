//go:build parity

package runner

import "testing"

func TestScenarioSchedulingGroup_DefaultsByDriver(t *testing.T) {
	cases := []struct {
		name string
		sc   Scenario
		want string
	}{
		{"tmux", Scenario{Driver: "interactive-tmux"}, "tmux"},
		{"tmux-extension", Scenario{Driver: "interactive-tmux", Env: EnvOverrides{PiExtensions: []string{"ext.ts"}}}, "tmux-ext"},
		{"tmux-faux-fixture-is-plain", Scenario{Driver: "interactive-tmux", Env: EnvOverrides{PiExtensions: []string{"../../testdata/test-faux-provider.ts"}}}, "tmux"},
		{"tmux-faux-plus-host-extension-stays-ext", Scenario{Driver: "interactive-tmux", Env: EnvOverrides{PiExtensions: []string{"../../testdata/test-faux-provider.ts", "parity-system.mjs"}}}, "tmux-ext"},
		{"ht", Scenario{Driver: "headless-terminal"}, "ht"},
		{"rpc", Scenario{Driver: "rpc-mode"}, "rpc"},
		{"cli", Scenario{Driver: "cli-mode"}, "process"},
		{"print", Scenario{Driver: "print-mode"}, "process"},
		{"print-extension", Scenario{Driver: "print-mode", Env: EnvOverrides{PigExtensions: []string{"ext.mjs"}}}, "process-ext"},
		{"print-faux-fixture-is-plain", Scenario{Driver: "print-mode", Env: EnvOverrides{PiExtensions: []string{"../../testdata/test-faux-provider.ts"}}}, "process"},
		{"serial-tag-wins", Scenario{Driver: "interactive-tmux", Tags: []string{"serial"}}, "exclusive"},
		{"explicit-override", Scenario{Driver: "interactive-tmux", Group: "slow-io"}, "slow-io"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sc.SchedulingGroup(); got != tc.want {
				t.Fatalf("SchedulingGroup() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseGroupLimits_Defaults(t *testing.T) {
	got, err := parseGroupLimits("")
	if err != nil {
		t.Fatalf("parseGroupLimits(empty) err = %v", err)
	}
	for _, key := range []string{"process", "process-ext", "tmux", "tmux-ext", "ht", "rpc"} {
		if got[key] <= 0 {
			t.Fatalf("group %q missing or non-positive: %v", key, got)
		}
	}
}

func TestParseGroupLimits_Custom(t *testing.T) {
	got, err := parseGroupLimits("process=4,tmux=1,rpc=3")
	if err != nil {
		t.Fatalf("parseGroupLimits custom err = %v", err)
	}
	if got["process"] != 4 || got["tmux"] != 1 || got["rpc"] != 3 {
		t.Fatalf("parseGroupLimits custom = %v, want process=4 tmux=1 rpc=3", got)
	}
}
