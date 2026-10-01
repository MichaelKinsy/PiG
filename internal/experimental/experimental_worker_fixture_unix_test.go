//go:build !windows

package experimental

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/internal/chord"
)

// This checks the fixture boundary, not another copy of the remote-runtime cases: a real child must persist the faux model and empty tool selection, publish the keyed fixture facet, and expose an OS-owned exit handle.
func TestExperimentalFauxWorkerFixtureUsesNativeProcessAndHarness(t *testing.T) {
	agentDir := setupExperimentalRemoteTest(t)
	installFauxSessionWorker(t)
	_, server := makeExperimentalServer(t)
	peer := attachExperimentalClient(t, server, "demo-1")
	pid, found := server.WorkerPids()["demo-1"]
	if !found || !processExists(t, pid) {
		t.Fatalf("fixture worker PID = %d, present = %v; want a live child", pid, found)
	}
	state := readExperimentalSessionState(t, filepath.Join(agentDir, "experimental", "sessions"), "demo-1")
	wantModel := session.ModelRef{Provider: "faux", ModelID: "faux-1"}
	if state.Model == nil || *state.Model != wantModel || len(state.ActiveTools) != 0 {
		t.Fatalf("fixture configuration = %#v, tools = %v; want %#v and empty tools", state.Model, state.ActiveTools, wantModel)
	}
	source, err := NewClientSessionServiceSource(peer, ClientServiceSourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := source.Dispose(context.Background()); err != nil {
			t.Error(err)
		}
	})
	catalogue, err := source.Catalogue(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	wantService := chord.ServiceCatalogueEntry{ServiceId: keyedProbeDefinition.Id(), Mode: chord.ServiceKeyed}
	if !slices.Contains(catalogue, wantService) {
		t.Fatalf("fixture catalogue = %v; missing %v", catalogue, wantService)
	}
	killWorkerProcess(t, pid)
	waitProcessExited(t, pid)
	// OS reaping and the manager's retirement of the worker are independent observations. Cleanup releases the still-attached Session, and a release that races the manager noticing the death is rejected with "Session worker disconnected during demand update" (upstream session-worker-manager.ts:#removeWorker), so join retirement before the test returns.
	waitExperimentalWorkerRetired(t, server, "demo-1")
	if processExists(t, pid) {
		t.Fatalf("fixture worker %d remains alive after actual process reaping", pid)
	}
}
