package extensionconformance

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The Lisp SDK serves the pi-tui and pi-coding-agent theme helpers of tui_probe_test.go and tui_theme_test.go through the shared Lisp SDK fixture
// (testdata/lisp-sdk-fixture/probes-tui.lisp), which carries the tui_probe and tui_theme_probe tools of the Python fixture. Only these rows run a
// Lisp placement on the model types rig: the fixture carries no other family's tools.

func loadTuiLisp(t *testing.T, r *modelTypesRig) {
	t.Helper()
	cfg := lispSurfaceFixtureConfig(t)
	loaded, failures := r.host.LoadAll(t.Context(), []subprocess.ExtConfig{cfg})
	applyQueuedRegistrations(r.host)
	if len(failures) != 0 || len(loaded) != 1 {
		t.Fatalf("load: %v", failures)
	}
	r.finish(loaded[0])
}

// tuiSDKs is the model types rig's SDKs and the Lisp SDK.
func tuiSDKs() []modelTypesSDK {
	return append(modelTypesSDKs(), modelTypesSDK{"lisp", map[string]modelTypesLoader{"strict": loadTuiLisp}})
}

func eachTuiPlacement(t *testing.T, body func(t *testing.T, r *modelTypesRig)) {
	t.Helper()
	eachTuiPlacementAfter(t, nil, nil, body)
}

// eachTuiPlacementAfter is eachModelTypesPlacementAfter over tuiSDKs.
func eachTuiPlacementAfter(t *testing.T, sdks []string, before func(t *testing.T, r *modelTypesRig), body func(t *testing.T, r *modelTypesRig)) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (starts SDK subprocesses)")
	}
	for _, kit := range tuiSDKs() {
		if len(sdks) != 0 && !slices.Contains(sdks, kit.name) {
			continue
		}
		for placement, load := range kit.placements {
			t.Run(kit.name+"-"+placement, func(t *testing.T) {
				r := newModelTypesRig(t)
				if before != nil {
					before(t, r)
				}
				load(t, r)
				body(t, r)
			})
		}
	}
}
