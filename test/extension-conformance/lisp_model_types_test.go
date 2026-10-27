package extensionconformance

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// loadModelTypesLisp loads the Lisp SDK harness fixture (testdata/lisp-sdk-fixture), which carries the tools of the model-types rows in its
// probes-*.lisp files, into the rig as the Python fixture above is: the typed reads, classify, generateImages and the virtual model tools are
// the model-types family's, and the lanes of the other families add theirs to the same extension.
func loadModelTypesLisp(t *testing.T, r *modelTypesRig) {
	t.Helper()
	cfg := lispFixtureConfig(t, "lisp-sdk-fixture", lispFixtureName)
	cfg.SourceInfo = hostSurfaceSource
	loaded, failures := r.host.LoadAll(t.Context(), []subprocess.ExtConfig{cfg})
	applyQueuedRegistrations(r.host)
	if len(failures) != 0 || len(loaded) != 1 {
		t.Fatalf("load: %v", failures)
	}
	r.finish(loaded[0])
}
