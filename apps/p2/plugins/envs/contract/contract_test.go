package contract

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Every contract of Pi's contract.ts has Pi's name, version and fingerprint (see sessions/contract).
func TestContractsFingerprintAsPi(t *testing.T) {
	const module = "plugins/envs/contract.ts"
	pi := oracle.Fingerprints(t, module)
	for name, ours := range map[string]*contracts.Contract{"EnvService": EnvService, "Envs": Envs, "SandboxSecrets": SandboxSecrets} {
		want, ok := pi[module+"#"+name]
		if !ok {
			t.Errorf("Pi exports no %s", name)
			continue
		}
		if ours.Name != want.Name || ours.Version != want.Version || ours.Fingerprint != want.Fingerprint {
			t.Errorf("%s = {%s v%d %s}, Pi has {%s v%d %s}", name, ours.Name, ours.Version, ours.Fingerprint, want.Name, want.Version, want.Fingerprint)
		}
	}
}

func TestEnvironmentPathShowsTheMachine(t *testing.T) {
	gpu := "gpu"
	if got := EnvironmentPath(&gpu, "/project"); got != "gpu:/project" {
		t.Errorf("EnvironmentPath(gpu) = %q", got)
	}
	if got := EnvironmentPath(nil, "/project"); got != "/project" {
		t.Errorf("EnvironmentPath(nil) = %q", got)
	}
	empty := ""
	if got := EnvironmentPath(&empty, "/project"); got != "/project" {
		t.Errorf("EnvironmentPath(empty) = %q", got)
	}
}
