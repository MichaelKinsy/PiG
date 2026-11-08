package envs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/test/p2/envs.test.ts "env configuration validates SSH fields, revisions are canonical, sessions are lazy"
// (the sessions-are-lazy half is RemoteSessionFor never connecting) and sandboxes.test.ts "sandbox configuration: validated
// alone and against its host".

func mustMarshal(t *testing.T, value any) string {
	t.Helper()
	data, err := chordjson.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func wantError(t *testing.T, err error, pattern string) {
	t.Helper()
	if err == nil {
		t.Errorf("no error, want one matching %q", pattern)
		return
	}
	if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(pattern)) {
		t.Errorf("error %q does not mention %q", err, pattern)
	}
}

const sshBox = `{"type":"ssh","host":"box"}`

func TestEnvConfigurationValidatesSSHFields(t *testing.T) {
	config, err := ParseEnvs("\ufeff{ // environment\n \"gpu\": { \"type\": \"ssh\", \"host\": \"box\", }, }")
	if err != nil {
		t.Fatal(err)
	}
	if got := mustMarshal(t, config); got != `{"gpu":`+sshBox+`}` {
		t.Errorf("ParseEnvs = %s", got)
	}
	for _, host := range []string{"-oProxyCommand=id", "a b", "a\nb"} {
		_, err := ValidateEnv(object(t, `{"type":"ssh","host":`+mustMarshal(t, host)+`}`))
		wantError(t, err, "host or user")
	}
	for text, pattern := range map[string]string{
		`{"type":"ssh","host":"box","port":0}`:                   "configuration",
		`{"type":"ssh","host":"box","password":"not-supported"}`: "configuration",
		`{"type":"ssh","host":"box","identityFile":"a\"b"}`:      "identity",
	} {
		_, err := ValidateEnv(object(t, text))
		wantError(t, err, pattern)
	}
	_, err = ParseEnvs(`{"GPU": {"type":"ssh","host":"box"}}`)
	wantError(t, err, "name")
}

func TestSandboxConfigurationIsValidatedAloneAndAgainstItsHost(t *testing.T) {
	box := object(t, boxJSON)
	validated, err := ValidateEnv(box)
	if err != nil || mustMarshal(t, validated) != boxJSON {
		t.Fatalf("ValidateEnv(box) = %v, %v", validated, err)
	}
	with := func(key, value string) *chordjson.Object {
		changed := object(t, boxJSON)
		changed.Set(key, object(t, `{"v":`+value+`}`).Value("v"))
		return changed
	}
	for _, mounts := range []string{
		`[{"host":"relative/path","guest":"/w"}]`,
		`[{"host":"~/a","guest":"relative"}]`,
		`[{"host":"~/a","guest":"/"}]`,
		`[{"host":"~/a","guest":"/w/../etc"}]`,
		`[{"host":"~/a","guest":"/w"},{"host":"~/b","guest":"/w"}]`,
	} {
		_, err := ValidateEnv(with("mounts", mounts))
		if err == nil {
			t.Errorf("mounts %s accepted", mounts)
			continue
		}
		lower := strings.ToLower(err.Error())
		if !strings.Contains(lower, "folder") && !strings.Contains(lower, "sandbox") && !strings.Contains(lower, "two folders") && !strings.Contains(lower, "configuration") {
			t.Errorf("mounts %s: error %q", mounts, err)
		}
	}
	_, err = ValidateEnv(with("allowHosts", `["a b"]`))
	wantError(t, err, "allowed host")
	_, err = ValidateEnv(with("host", `"Not An Env"`))
	wantError(t, err, "host environment")
	_, err = ValidateEnv(with("image", `"-rf"`))
	wantError(t, err, "image")
	// SSH fields are not part of a sandbox: its connection goes through its host.
	_, err = ValidateEnv(with("user", `"root"`))
	wantError(t, err, "configuration")
	// A writable folder must not contain or be inside ~/.pi; the launcher refuses these too.
	_, err = ValidateEnv(with("mounts", `[{"host":"~/.pi/x","guest":"/w"}]`))
	wantError(t, err, "~/.pi")
	_, err = ValidateEnv(with("mounts", `[{"host":"~/a","guest":"/opt/pi-env/x"}]`))
	wantError(t, err, "used by p2")

	_, err = ParseEnvs(`{"box1":` + boxJSON + `}`)
	wantError(t, err, "not an SSH environment")
	_, err = ParseEnvs(`{"other":` + boxJSON + `,"slayer":{"type":"gondolin","host":"other"}}`)
	wantError(t, err, "not an SSH")
	both := `{"slayer":` + slayerJSON + `,"box1":` + boxJSON + `}`
	envs, err := ParseEnvs(both)
	if err != nil {
		t.Fatal(err)
	}
	if mustMarshal(t, envs) != both {
		t.Errorf("ParseEnvs(both) = %s", mustMarshal(t, envs))
	}
	if !IsSandbox(envs, "box1") || IsSandbox(envs, "slayer") || IsSandbox(envs, "constructor") {
		t.Error("IsSandbox does not follow the configuration's type")
	}
	if on := SandboxesOn(envs, "slayer"); len(on) != 1 || on[0].Name != "box1" {
		t.Errorf("SandboxesOn = %v", on)
	}
}

func TestSaveEnvKeepsFilesPrivateSerializesAndChecksSandboxHosts(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	box, slayer := object(t, boxJSON), object(t, slayerJSON)
	wantError(t, SaveEnv(ctx, directory, "box1", box), "not an SSH environment")
	if err := SaveEnv(ctx, directory, "slayer", slayer); err != nil {
		t.Fatal(err)
	}
	if err := SaveEnv(ctx, directory, "box1", box); err != nil {
		t.Fatal(err)
	}
	wantError(t, SaveEnv(ctx, directory, "slayer", nil), `Sandboxes run on "slayer": box1`)
	if err := SaveEnv(ctx, directory, "box1", nil); err != nil {
		t.Fatal(err)
	}
	if err := SaveEnv(ctx, directory, "slayer", nil); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadEnvs(ctx, directory)
	if err != nil || loaded.Len() != 0 {
		t.Fatalf("LoadEnvs = %v, %v", loaded, err)
	}
	wantError(t, SaveEnv(ctx, directory, "Bad Name", slayer), "names use")

	// Concurrent saves do not lose each other, and the file is 0600.
	config := object(t, sshBox)
	var wg sync.WaitGroup
	for _, name := range []string{"one", "two"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := SaveEnv(ctx, directory, name, config); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	loaded, err = LoadEnvs(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	if keys := loaded.Keys(); len(keys) != 2 {
		t.Errorf("saved %v, want one and two", keys)
	}
	info, err := os.Stat(filepath.Join(directory, "envs.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("envs.json mode = %v, %v; want 0600", info, err)
	}
}

func TestRemoteSessionNeverConnectsWhenBuilt(t *testing.T) {
	directory := t.TempDir()
	for name, text := range map[string]string{"gpu": sshBox, "box1": boxJSON} {
		cwd := "/project"
		session := RemoteSessionFor(directory, name, object(t, text), cwd, ConnectOptions{})
		if session.Remote() != nil {
			t.Errorf("%s: constructing a remote session connected", name)
		}
		if session.Env.Cwd() != cwd {
			t.Errorf("%s: cwd = %q", name, session.Env.Cwd())
		}
		session.Connection.Close()
	}
	if got := SSHOptions(directory, "gpu", object(t, sshBox)).HostKeyAlias; got != "pi-env-gpu" {
		t.Errorf("HostKeyAlias = %q", got)
	}
}
