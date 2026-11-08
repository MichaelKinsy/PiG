package hostenv

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/envs"
)

const (
	slayerJSON = `{"type":"ssh","host":"slayer.example"}`
	boxJSON    = `{"type":"gondolin","host":"slayer","mounts":[{"host":"~/sbx/box1","guest":"/workspace"}],"allowHosts":["registry.npmjs.org","*.github.com"]}`
)

func TestResolveRefusesWhatTheTerminalsHostMustNotConnectTo(t *testing.T) {
	directory := t.TempDir()
	ctx := t.Context()
	if _, err := Resolve(ctx, directory, "nothing"); err == nil || err.Error() != "Unknown environment nothing" {
		t.Fatalf("absent envs.json: err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "envs.json"), []byte(`{"slayer":`+slayerJSON+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(ctx, directory, "other"); err == nil || err.Error() != "Unknown environment other" {
		t.Fatalf("err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "envs.json"), []byte(`{"box1":`+boxJSON+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(ctx, directory, "box1"); err == nil || err.Error() != `Sandbox box1 runs on "slayer", which is not an SSH environment` {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveGivesTheRevisionAndShellEnvironment(t *testing.T) {
	directory := t.TempDir()
	content := `{"slayer":{"type":"ssh","host":"slayer.example","shellPath":"/bin/zsh","shellEnv":{"A":"1","SHELL":"/x"}},"plain":{"type":"ssh","host":"p.example","shellEnv":{"B":"2"}},"box1":` + boxJSON + `}`
	if err := os.WriteFile(filepath.Join(directory, "envs.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	configs, err := envs.LoadEnvs(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]map[string]string{
		"slayer": {"A": "1", "SHELL": "/bin/zsh"},
		"plain":  {"B": "2"},
		"box1":   {},
	} {
		remote, err := Resolve(t.Context(), directory, name)
		if err != nil {
			t.Fatal(err)
		}
		if remote.Revision != envs.ConnectionRevision(configs, name) || remote.Revision == "" || !reflect.DeepEqual(remote.ShellEnv, want) || remote.Connect == nil {
			t.Errorf("%s: %+v", name, remote)
		}
	}
}
