package envs

import (
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports the launcher tests of apps/p2/test/p2/sandboxes.test.ts. Expected values are what Pi's sandboxArgument,
// sandboxRevision, runLauncher and launcherId print for the same inputs and files.

func decodeArgument(t *testing.T, argument string) string {
	t.Helper()
	text, err := base64.StdEncoding.DecodeString(argument)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func TestLauncherArgumentsAllowedHostsAreAlwaysAListValuesAreQuoted(t *testing.T) {
	box := object(t, boxJSON)
	if got := SandboxArgument(box, nil); got != "eyJtb3VudHMiOlt7Imhvc3QiOiJ+L3NieC9ib3gxIiwiZ3Vlc3QiOiIvd29ya3NwYWNlIn1dLCJhbGxvd0hvc3RzIjpbInJlZ2lzdHJ5Lm5wbWpzLm9yZyIsIiouZ2l0aHViLmNvbSJdLCJkaXNrR2IiOjMyfQ==" {
		t.Errorf("argument = %s", got)
	}
	if got := decodeArgument(t, SandboxArgument(box, nil)); got != `{"mounts":[{"host":"~/sbx/box1","guest":"/workspace"}],"allowHosts":["registry.npmjs.org","*.github.com"],"diskGb":32}` {
		t.Errorf("decoded = %s", got)
	}
	// Gondolin allows every host when the list is omitted: an unset list must reach it as an empty one.
	unset := object(t, `{"type":"gondolin","host":"slayer","mounts":[{"host":"~/sbx/box1","guest":"/workspace"}]}`)
	if got := decodeArgument(t, SandboxArgument(unset, nil)); !strings.Contains(got, `"allowHosts":[]`) {
		t.Errorf("unset list: %s", got)
	}
	if got := decodeArgument(t, SandboxArgument(object(t, `{"type":"gondolin","host":"slayer"}`), nil)); got != `{"mounts":[],"allowHosts":[],"diskGb":32}` {
		t.Errorf("bare: %s", got)
	}
	empty := object(t, `{"type":"gondolin","host":"slayer","mounts":[{"host":"~/sbx/box1","guest":"/workspace"}],"allowHosts":[]}`)
	if SandboxRevision(box, nil) != "97158cd99d65914a" || SandboxRevision(empty, nil) != "2f43e2c57f7987dc" {
		t.Errorf("revisions %s %s", SandboxRevision(box, nil), SandboxRevision(empty, nil))
	}
	if got := RunLauncher("/home/a b", []string{"daemon", "it's"}); got != `sh '/home/a b/p2-sandbox.sh' 'daemon' 'it'\''s'` {
		t.Errorf("RunLauncher = %s", got)
	}
}

func TestLauncherArgumentWithEveryOptionAndSecrets(t *testing.T) {
	full := object(t, `{"type":"gondolin","host":"slayer","mounts":[{"host":"~/sbx/box1","guest":"/workspace"}],"allowHosts":["registry.npmjs.org","*.github.com"],"image":"alpine:3","cpus":4,"memoryMb":2048,"diskGb":8}`)
	meta := []SecretMeta{{Name: "GITHUB_TOKEN", Hosts: []string{"*.github.com"}, Version: "abc123", Aliases: []string{"GH_TOKEN"}, GitHosts: []string{"github.com"}}}
	const want = "eyJpbWFnZSI6ImFscGluZTozIiwibW91bnRzIjpbeyJob3N0Ijoifi9zYngvYm94MSIsImd1ZXN0IjoiL3dvcmtzcGFjZSJ9XSwiYWxsb3dIb3N0cyI6WyJyZWdpc3RyeS5ucG1qcy5vcmciLCIqLmdpdGh1Yi5jb20iXSwiY3B1cyI6NCwibWVtb3J5TWIiOjIwNDgsImRpc2tHYiI6OCwic2VjcmV0cyI6W3sibmFtZSI6IkdJVEhVQl9UT0tFTiIsImhvc3RzIjpbIiouZ2l0aHViLmNvbSJdLCJ2ZXJzaW9uIjoiYWJjMTIzIiwiYWxpYXNlcyI6WyJHSF9UT0tFTiJdLCJnaXRIb3N0cyI6WyJnaXRodWIuY29tIl19XX0="
	if got := SandboxArgument(full, meta); got != want {
		t.Errorf("argument = %s", decodeArgument(t, got))
	}
	box := object(t, boxJSON)
	if got := SandboxRevision(box, meta); got != "0da2d8a7659bcacb" || got == SandboxRevision(box, nil) {
		t.Errorf("revision with secrets = %s", got)
	}
}

func TestLauncherIDIsThePackagedFilesContentHash(t *testing.T) {
	launcher, err := DefaultLauncher()
	if err != nil {
		t.Fatal(err)
	}
	if launcher.ID != "12de40a57a5f20f9" {
		t.Errorf("ID = %s, want Pi's for the same files", launcher.ID)
	}
	// The same files from a directory.
	again, err := LoadLauncher(os.DirFS("sandbox"))
	if err != nil || again.ID != launcher.ID {
		t.Fatalf("directory launcher %v, %v", again, err)
	}
	if _, err := LoadLauncher(os.DirFS(t.TempDir())); err == nil {
		t.Fatal("a directory without the files loads")
	}
}

func TestSandboxKeyNamesTheInstallation(t *testing.T) {
	first, second := SandboxKey("/data/a", "box1"), SandboxKey("/data/b", "box1")
	if first == second || !strings.HasSuffix(first, ".box1") || len(first) != len("12345678.box1") {
		t.Fatalf("keys %s, %s", first, second)
	}
	if SandboxKey("/data/a", "box1") != first {
		t.Fatal("not stable")
	}
}

// fakeSSH puts an `ssh` on the PATH that runs its last argument, the remote command, locally with the same standard input.
func fakeSSH(t *testing.T) env.SshTarget {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte("#!/bin/sh\nfor last; do :; done\nexec sh -c \"$last\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return env.SshTarget{Host: "fake.example", KnownHostsFile: filepath.Join(t.TempDir(), "known_hosts"), HostKeyAlias: "pi-env-fake"}
}

func TestDeployLauncherWritesChecksAndRepairsTheDirectory(t *testing.T) {
	target := fakeSSH(t)
	home := t.TempDir()
	remote := env.RemotePlatform{Platform: "linux", Arch: "x64", Home: home}
	launcher, err := DefaultLauncher()
	if err != nil {
		t.Fatal(err)
	}
	directory, err := DeployLauncher(t.Context(), target, remote, launcher)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".pi", "p2", "sandbox", launcher.ID); directory != want {
		t.Fatalf("directory = %s, want %s", directory, want)
	}
	for _, file := range launcher.files {
		got, err := os.ReadFile(filepath.Join(directory, file.name))
		if err != nil || string(got) != string(file.content) {
			t.Fatalf("%s: %v", file.name, err)
		}
	}
	if info, err := os.Stat(filepath.Join(directory, "p2-sandbox.sh")); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("launcher mode %v, %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(directory, ".complete")); err != nil {
		t.Fatal(err)
	}
	// A changed file is written again; an intact directory is left alone.
	marker := filepath.Join(directory, "stray")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := DeployLauncher(t.Context(), target, remote, launcher); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("an intact directory was rewritten: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "p2-sandbox.mjs"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := DeployLauncher(t.Context(), target, remote, launcher); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(directory, "p2-sandbox.mjs")); string(got) == "tampered" {
		t.Fatal("a changed file was kept")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a repaired directory kept a stray file")
	}
	// Directories of other versions stay while recent, and go when unused for 30 days.
	other := filepath.Join(home, ".pi", "p2", "sandbox", "0123456789abcdef")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := DeployLauncher(t.Context(), target, remote, launcher); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("a recent version was removed: %v", err)
	}
	old := filepath.Join(home, ".pi", "p2", "sandbox", "fedcba9876543210")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := exec.Command("touch", "-d", "40 days ago", old).Run(); err != nil {
		t.Skipf("no touch -d: %v", err)
	}
	if _, err := DeployLauncher(t.Context(), target, remote, launcher); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Fatal("a version unused for 30 days stayed")
	}
}

func TestDeployedCommandReadsTheLastLineAsJSON(t *testing.T) {
	target := fakeSSH(t)
	directory := t.TempDir()
	script := "#!/bin/sh\ncat >&2\necho noise\necho '{\"state\":\"stopped\"}'\n"
	if err := os.WriteFile(filepath.Join(directory, "p2-sandbox.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// The script runs through `sh <file>`: stdin is the secrets, and the answer is its last line.
	got, err := DeployedCommand(t.Context(), target, directory, []string{"status", "k"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := chordjson.ObjectOf("state", "stopped"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
	if err := os.WriteFile(filepath.Join(directory, "p2-sandbox.sh"), []byte("echo "+strings.Repeat("x", 300)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = DeployedCommand(t.Context(), target, directory, []string{"status"}, "")
	if err == nil || err.Error() != "Unexpected answer from the sandbox launcher: "+strings.Repeat("x", 200) {
		t.Fatalf("err = %v", err)
	}
	// A failing command is its stderr; without stderr, its exit code.
	if err := os.WriteFile(filepath.Join(directory, "p2-sandbox.sh"), []byte("echo 'no kvm' >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err = DeployedCommand(t.Context(), target, directory, []string{"start", "k", "a"}, "{}"); err == nil || err.Error() != "no kvm" {
		t.Fatalf("err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "p2-sandbox.sh"), []byte("exit 4\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err = DeployedCommand(t.Context(), target, directory, []string{"stop", "k"}, ""); err == nil || err.Error() != "ssh exited with code 4" {
		t.Fatalf("err = %v", err)
	}
}

func TestStartFeedsTheSecretsOnStandardInput(t *testing.T) {
	target := fakeSSH(t)
	directory := t.TempDir()
	received := filepath.Join(directory, "stdin")
	script := "#!/bin/sh\ncat > " + received + "\necho '{\"state\":\"running\"}'\n"
	if err := os.WriteFile(filepath.Join(directory, "p2-sandbox.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := DeployedCommand(t.Context(), target, directory, []string{"start", "k", "arg"}, `{"A":"secret"}`); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(received); string(got) != `{"A":"secret"}` {
		t.Fatalf("stdin = %q", got)
	}
}

// Ports "p2-sandbox.sh check reports the host as the manager expects" (LauncherCheckSchema).
func TestLauncherCheckReportsTheHostAsTheManagerExpects(t *testing.T) {
	output, err := exec.Command("sh", filepath.Join("sandbox", "p2-sandbox.sh"), "check").Output()
	if err != nil {
		t.Fatal(err)
	}
	value, err := chordjson.Decode([]byte(strings.TrimSpace(string(output))))
	if err != nil {
		t.Fatalf("%s: %v", output, err)
	}
	report := value.(*chordjson.Object)
	for key, kind := range map[string]string{"runtime": "string", "system": "string", "arch": "string", "kvm": "bool", "kvmDevice": "bool", "qemu": "string", "node": "string"} {
		switch report.Value(key).(type) {
		case string:
			if kind != "string" {
				t.Errorf("%s: %v", key, report.Value(key))
			}
		case bool:
			if kind != "bool" {
				t.Errorf("%s: %v", key, report.Value(key))
			}
		default:
			t.Errorf("%s: %v in %s", key, report.Value(key), output)
		}
	}
	if runtime := report.Value("runtime"); runtime != "native" && runtime != "none" {
		t.Errorf("runtime = %v", runtime)
	}
}
