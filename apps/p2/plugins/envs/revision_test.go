package envs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports the connection revision test of apps/p2/test/p2/sandboxes.test.ts. The expected values are what Pi's
// envRevision and connectionRevision (apps/p2/src/runtime/environments.ts, plugins/envs/connection-revision.ts) print for the
// same inputs.

const (
	slayerJSON = `{"type":"ssh","host":"slayer.example"}`
	boxJSON    = `{"type":"gondolin","host":"slayer","mounts":[{"host":"~/sbx/box1","guest":"/workspace"}],"allowHosts":["registry.npmjs.org","*.github.com"]}`
)

func object(t testing.TB, text string) *chordjson.Object {
	t.Helper()
	value, err := chordjson.Decode([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value.(*chordjson.Object)
}

func configs(t testing.TB, slayer string) *chordjson.Object {
	t.Helper()
	return object(t, `{"slayer":`+slayer+`,"box1":`+boxJSON+`}`)
}

func TestEnvRevisionMatchesPi(t *testing.T) {
	for _, each := range []struct{ name, config, want string }{
		{"slayer", slayerJSON, "466b93c2eba8"},
		{"box", boxJSON, "9849c7b207b1"},
		// Keys are sorted (integer-like ones first, as Object.fromEntries orders them); an array and what is in it keeps its order.
		{"reorder", `{"host":"slayer.example","type":"ssh","shellEnv":{"b":"1","a":"2","10":"x","2":"y"}}`, "88df5ef488eb"},
		{"array", `{"type":"ssh","host":"h","list":[{"z":1,"a":2}]}`, "917484f5ea0b"},
	} {
		if got := EnvRevision(object(t, each.config)); got != each.want {
			t.Errorf("%s: EnvRevision = %s, want %s", each.name, got, each.want)
		}
	}
}

func TestASandboxsConnectionRevisionFollowsItsHostsConfigurationToo(t *testing.T) {
	envs := configs(t, slayerJSON)
	before := ConnectionRevision(envs, "box1")
	if before != "9849c7b207b1+466b93c2eba8" || ConnectionRevision(envs, "box1") != before {
		t.Fatalf("revision = %s", before)
	}
	if got := ConnectionRevision(configs(t, `{"type":"ssh","host":"slayer.example","port":2222}`), "box1"); got != "9849c7b207b1+264e08accf78" || got == before {
		t.Errorf("host edited: %s", got)
	}
	cpus := object(t, `{"slayer":`+slayerJSON+`,"box1":{"type":"gondolin","host":"slayer","mounts":[{"host":"~/sbx/box1","guest":"/workspace"}],"allowHosts":["registry.npmjs.org","*.github.com"],"cpus":4}}`)
	if got := ConnectionRevision(cpus, "box1"); got != "d0701f868829+466b93c2eba8" {
		t.Errorf("sandbox edited: %s", got)
	}
	if got := ConnectionRevision(envs, "slayer"); got != EnvRevision(object(t, slayerJSON)) {
		t.Errorf("ssh: %s", got)
	}
	// A sandbox whose host is gone has an empty host part; an unknown environment has no revision.
	if got := ConnectionRevision(object(t, `{"box1":`+boxJSON+`}`), "box1"); got != "9849c7b207b1+" {
		t.Errorf("orphan: %s", got)
	}
	if got := ConnectionRevision(envs, "missing"); got != "" {
		t.Errorf("unknown: %q", got)
	}
}

func TestSSHOptionsAndIdentifiers(t *testing.T) {
	config := object(t, `{"type":"ssh","host":"h.example","user":"me","port":2222,"identityFile":"/k","loginShell":true,"shellPath":"/bin/zsh"}`)
	options := SSHOptions("/data", "box", config)
	if options.Host != "h.example" || *options.User != "me" || *options.Port != 2222 || *options.IdentityFile != "/k" || !options.LoginShell ||
		options.KnownHostsFile != filepath.Join("/data", "ssh", "known_hosts") || options.HostKeyAlias != "pi-env-box" {
		t.Fatalf("options = %+v", options)
	}
	plain := SSHOptions("/data", "box", object(t, slayerJSON))
	if plain.User != nil || plain.Port != nil || plain.IdentityFile != nil || plain.LoginShell {
		t.Fatalf("plain = %+v", plain)
	}
	if got := EnvID("slayer", object(t, slayerJSON)); got != "pi-env:slayer@466b93c2eba8" {
		t.Errorf("EnvID = %s", got)
	}
}

func TestSandboxHostMustBeAnSSHEnvironment(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "envs.json"), []byte(`{"slayer":`+slayerJSON+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	host, err := SandboxHost(t.Context(), directory, "box1", "slayer")
	if err != nil || host.Host != "slayer.example" || host.HostKeyAlias != "pi-env-slayer" {
		t.Fatalf("host = %+v, %v", host, err)
	}
	_, err = SandboxHost(t.Context(), directory, "box1", "gone")
	if err == nil || err.Error() != `Sandbox box1 runs on "gone", which is not an SSH environment` {
		t.Fatalf("err = %v", err)
	}
}
