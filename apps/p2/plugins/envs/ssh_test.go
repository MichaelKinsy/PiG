package envs

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Ports the SSH half of envs.test.ts "remote sessions use SSH for directories/tools/uploads/prompt resources, trust host keys
// and confirm shares" that belongs to the envs plugin: a local sshd, the owner's keys, host-key trust, the connection test,
// directory browsing and working directories. (Tools, uploads, prompt resources, shares, terminals and subagents are the
// chat, files, prompt, sharing, terminals and subagents plugins' halves of the same Pi case.)

func lookSshd() string {
	if path, err := exec.LookPath("sshd"); err == nil {
		return path
	}
	if _, err := os.Stat("/usr/sbin/sshd"); err == nil {
		return "/usr/sbin/sshd"
	}
	return ""
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func TestSSHEnvironmentTrustsHostKeysConnectsAndBrowsesDirectories(t *testing.T) {
	sshd := lookSshd()
	if sshd == "" {
		t.Skip("no local sshd")
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("no ssh-keygen")
	}
	if _, err := env.LocalDaemon(); err != nil {
		t.Skipf("no packaged pi-env daemon: %v", err)
	}
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	hostKey := filepath.Join(root, "host_key")
	if output, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", hostKey).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, output)
	}
	authorized := filepath.Join(root, "authorized_keys")
	if err := os.WriteFile(authorized, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	config := strings.Join([]string{
		fmt.Sprintf("Port %d", port), "ListenAddress 127.0.0.1", "HostKey " + hostKey, "AuthorizedKeysFile " + authorized,
		"PidFile " + filepath.Join(root, "sshd.pid"), "PasswordAuthentication no", "KbdInteractiveAuthentication no",
		"StrictModes no", "SetEnv HOME=" + home, "",
	}, "\n")
	configPath := filepath.Join(root, "sshd_config")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	server := exec.Command(sshd, "-D", "-e", "-f", configPath)
	if err := server.Start(); err != nil {
		t.Skipf("sshd does not start here: %v", err)
	}
	t.Cleanup(func() {
		_ = server.Process.Kill()
		_ = server.Wait()
	})
	if err := p2test.Poll(func() (bool, error) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err != nil {
			return false, nil
		}
		_ = conn.Close()
		return true, nil
	}, 10*time.Second, "local sshd"); err != nil {
		t.Skipf("sshd does not listen here: %v", err)
	}

	f := newEnvsFixture(t)
	envs := f.owner(t, owner)
	call := func(method string, pairs ...any) (delta.JsonValue, error) {
		return envs.Call(method, delta.JsonObjectOf(pairs...), p2test.Background)
	}
	key, err := call("generateKey", "name", "box")
	if err != nil {
		t.Fatal(err)
	}
	publicKey := stringOf(objectOf(key).Value("publicKey"))
	if !strings.HasPrefix(publicKey, "ssh-ed25519 ") {
		t.Fatalf("public key = %q", publicKey)
	}
	if err := os.WriteFile(authorized, []byte(publicKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sshConfig := object(t, "{}")
	for k, v := range map[string]any{"type": "ssh", "host": "127.0.0.1", "port": float64(port), "user": current.Username, "identityFile": objectOf(key).Value("identityFile")} {
		sshConfig.Set(k, v)
	}
	if _, err := call("save", "name", "box", "config", sshConfig); err != nil {
		t.Fatal(err)
	}
	again, err := call("generateKey", "name", "box")
	if err != nil || objectOf(again).Value("publicKey") != publicKey {
		t.Fatalf("generating twice = %v, %v", again, err)
	}

	// An unknown host key is not trusted: the test shows what the host presents.
	untrusted, err := call("test", "name", "box")
	if err != nil {
		t.Fatal(err)
	}
	if objectOf(untrusted).Value("ok") != false || objectOf(objectOf(untrusted).Value("hostKey")).Value("state") != "unknown" {
		t.Fatalf("untrusted test = %s", mustMarshal(t, untrusted))
	}
	output, err := exec.Command("ssh-keygen", "-lf", hostKey+".pub").Output()
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := strings.Fields(string(output))[1]
	hostKeyInfo := objectOf(objectOf(untrusted).Value("hostKey"))
	if prints, _ := hostKeyInfo.Value("fingerprints").([]any); !strings.Contains(fmt.Sprint(prints), fingerprint) {
		t.Errorf("fingerprints %v do not include %s", prints, fingerprint)
	}
	if _, err := call("acceptHostKey", "name", "box", "lines", hostKeyInfo.Value("lines")); err != nil {
		t.Fatal(err)
	}
	trusted, err := call("test", "name", "box")
	if err != nil {
		t.Fatal(err)
	}
	if objectOf(trusted).Value("ok") != true || objectOf(trusted).Value("home") != home {
		t.Fatalf("trusted test = %s", mustMarshal(t, trusted))
	}
	// The test is listed with the environment while its configuration is the tested one.
	if _, err := p2test.Until(envs.Member("envs"), func(value delta.JsonValue) bool {
		rows, _ := value.([]any)
		return len(rows) == 1 && objectOf(objectOf(rows[0]).Value("test")).Value("ok") == true
	}, 0); err != nil {
		t.Fatal(err)
	}

	// The folder picker warms every environment's connection first.
	if answer, err := call("warm"); err != nil || answer != nil {
		t.Fatalf("warm = %v, %v", answer, err)
	}
	listing, err := call("listDirectory", "env", "box", "path", "~")
	if err != nil || objectOf(listing).Value("path") != home {
		t.Fatalf("listDirectory = %v, %v", listing, err)
	}
	if err := os.MkdirAll(filepath.Join(home, "project", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, filepath.Join(home, "project"), filepath.Join(home, "link"))
	if err := os.WriteFile(filepath.Join(home, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	listing, err = call("listDirectory", "env", "box", "path", "~")
	if err != nil {
		t.Fatal(err)
	}
	dirs, _ := objectOf(listing).Value("directories").([]any)
	if fmt.Sprint(dirs) != fmt.Sprint([]any{filepath.Join(home, ".pi"), filepath.Join(home, "link"), filepath.Join(home, "project")}) {
		t.Errorf("directories = %v (folders and links to folders, sorted, no files; .pi is the deployed daemon)", dirs)
	}
	if objectOf(listing).Value("truncated") != false {
		t.Error("a small listing is truncated")
	}
	_, err = call("listDirectory", "env", "box", "path", "~/file.txt")
	wantError(t, err, "not a directory")

	// A session's working directory: resolved on the environment and created on request.
	parts, err := f.managerParts(t)
	if err != nil {
		t.Fatal(err)
	}
	created, err := parts.WorkingDirectory(context.Background(), "box", "~/made/deeper", true)
	if err != nil || created != filepath.Join(home, "made", "deeper") {
		t.Fatalf("WorkingDirectory(create) = %q, %v", created, err)
	}
	if _, err := parts.WorkingDirectory(context.Background(), "box", "~/file.txt", false); err == nil {
		t.Error("a file is a working directory")
	}

	// Uploading a key for another environment and deleting it again.
	uploaded, err := call("uploadKey", "name", "copy", "key", readFile(t, objectOf(key).Value("identityFile")))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(strings.Fields(stringOf(objectOf(uploaded).Value("publicKey")))[:2], " ") != strings.Join(strings.Fields(publicKey)[:2], " ") {
		t.Error("the uploaded key's public key differs")
	}
	if _, err := call("delete", "name", "copy"); err != nil {
		t.Fatal(err)
	}

	// Forgetting the host key makes it unknown again; deleting removes the environment and its key.
	if _, err := call("forgetHostKey", "name", "box"); err != nil {
		t.Fatal(err)
	}
	forgotten, err := call("test", "name", "box")
	if err != nil || objectOf(forgotten).Value("ok") != false || objectOf(objectOf(forgotten).Value("hostKey")).Value("state") != "unknown" {
		t.Fatalf("forgotten test = %v, %v", forgotten, err)
	}
	if _, err := call("delete", "name", "box"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "ssh", "keys", "box")); !os.IsNotExist(err) {
		t.Errorf("the deleted environment's key stays: %v", err)
	}
}

func readFile(t *testing.T, path any) string {
	t.Helper()
	data, err := os.ReadFile(stringOf(path))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// managerParts is what other plugins read from `EnvManagers`: the manager of the fixture's server.
func (f *envsFixture) managerParts(t *testing.T) (interface {
	WorkingDirectory(context.Context, string, string, bool) (string, error)
}, error) {
	t.Helper()
	manager, err := NewManager(f.dataDir, func(context.Context, string) (int, error) { return 0, nil }, nil, nil), error(nil)
	t.Cleanup(manager.Close)
	return manager, err
}
