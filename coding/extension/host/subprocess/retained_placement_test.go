package subprocess

import (
	"testing"
)

// PiG's isolation setting has no Pi counterpart: Pi loads every extension into one runtime. A process holds module state only for the extensions placed in it, so an extension whose placement changes (isolated to shared-ok or back) or that Host.Load started outside the plan gets a new process and a new module on the next reload, and its old process ends. An unchanged placement keeps the process (TestNodeReloadReinvokesFactoriesInTheRetainedProcessWithPiLoaderRules).
func TestReloadAcrossAPlacementChangeStartsAFreshProcess(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		start func(t *testing.T, h *Host, cfg ExtConfig)
		from  string
		to    string
	}{
		{name: "isolated to shared-ok", from: "isolated", to: "", start: loadAllStart},
		{name: "shared-ok to isolated", from: "", to: "isolated", start: loadAllStart},
		{name: "Host.Load to shared-ok", from: "", to: "", start: func(t *testing.T, h *Host, cfg ExtConfig) {
			t.Helper()
			if _, err := h.Load(t.Context(), cfg); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodeCellRequireNode(t)
			root := t.TempDir()
			cfg, _ := retainedNodeExtension(t, root, "placed.mjs", false)
			cfg.Isolation = tc.from
			h := NewHost(t.TempDir())
			t.Cleanup(func() { h.Shutdown("test done") })
			next := cfg
			next.Isolation = tc.to
			h.SetConfigLoader(func() ([]ExtConfig, error) { return []ExtConfig{next}, nil })
			tc.start(t, h, cfg)
			before := retainedProbeOf(t, h, cfg.Name)
			if _, err := h.Reload(t.Context()); err != nil {
				t.Fatal(err)
			}
			after := retainedProbeOf(t, h, cfg.Name)
			if after.Pid == before.Pid || after.Calls != 1 {
				t.Errorf("probe after the placement change = %+v (before %+v), want a new process with a new module", after, before)
			}
			if processes := nodeCellProcessesForMarker(t, root); len(processes) != 1 {
				t.Errorf("Node processes = %v, want only the process placed by the reload", processes)
			}
		})
	}
}

func loadAllStart(t *testing.T, h *Host, cfg ExtConfig) {
	t.Helper()
	if _, errs := h.LoadAll(t.Context(), []ExtConfig{cfg}); len(errs) > 0 {
		t.Fatal(errs)
	}
}
