package envs

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// The worker facet's `remote` provider (worker.ts): which environments are remote, how it describes them, which are sandboxes,
// and that an environment that is not configured is refused rather than run locally.
func TestTheWorkersRemoteProviderRefusesUnconfiguredEnvironmentsAndDescribesTheRest(t *testing.T) {
	f := newEnvsFixture(t)
	f.write(t, `{"gpu":{"type":"ssh","host":"gpu.example"},"box":{"type":"gondolin","host":"gpu"}}`)
	parts := &p2runtime.WorkerParts{Liveness: p2runtime.NewLiveness()}
	parts.Init.DataDir = f.dataDir
	worker := f.WorkerHost("s1", 1, p2test.WorkerFacet(p2test.HarnessParts(p2test.OpenHarness(t)), parts, nil), workerFacet)

	source, err := worker.Registry(p2test.Background, p2runtime.HarnessEnvironments.Registry, host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var remote p2runtime.HarnessEnvironmentEntry
	if err := p2test.Poll(func() (bool, error) {
		table, err := p2runtime.HarnessEnvironments.Table(source)
		if err != nil {
			return false, nil
		}
		entry, ok := table.Get("remote")
		remote = entry
		return ok, nil
	}, 0, "the remote environment provider"); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = remote.Open(ctx, "missing")
	wantError(t, err, `Environment "missing" is not configured; refusing local execution`)
	for name, want := range map[string]string{
		"gpu":     `the remote environment "gpu" (SSH host gpu.example)`,
		"box":     `the sandbox "box" (a VM on the environment "gpu"; you cannot leave it, and only its own folders and allowed hosts are reachable)`,
		"missing": "",
	} {
		text, ok, err := remote.Describe(ctx, name)
		if err != nil || text != want || ok != (want != "") {
			t.Errorf("Describe(%s) = %q, %v, %v; want %q", name, text, ok, err, want)
		}
	}
	// The observed envs.json decides which are sandboxes (never the name).
	if err := p2test.Poll(func() (bool, error) { return remote.Sandbox("box"), nil }, 0, "the observer to read envs.json"); err != nil {
		t.Fatal(err)
	}
	if remote.Sandbox("gpu") || remote.Sandbox("missing") || remote.Sandbox("constructor") {
		t.Error("an SSH environment, or a name that is no environment, is a sandbox")
	}
	// Opening one never connects: the machine's environments are made for any directory.
	machine, err := remote.Open(ctx, "gpu")
	if err != nil {
		t.Fatal(err)
	}
	if cwd := machine.At("/project"); cwd == nil {
		t.Error("the machine makes no environment")
	}
	machine.Close()
}
