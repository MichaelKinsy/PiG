package envs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// Ports apps/p2/plugins/envs/manager.ts: owner management of envs.json, host keys, connection tests and remote directory
// browsing for new sessions. Methods run on the callers' goroutines; the manager's own state is under one mutex and never
// holds host state.

const (
	// idleAfter is how long directory browsing keeps a connection after its last use: a new one costs several ssh handshakes.
	idleAfter = 600 * time.Second
	// listingScanLimit is the most entries one listing reads; the first 1,000 directories are shown, as for local ones.
	listingScanLimit = 10_000
	listingShown     = 1000
	// maxKeyBytes is the largest private key accepted by upload.
	maxKeyBytes = 16 * 1024
)

type browser struct {
	revision string
	session  RemoteSession
	timer    *time.Timer
}

// Manager is the server's SSH manager (`EnvManagers`).
type Manager struct {
	directory string
	uses      func(ctx context.Context, name string) (int, error)
	launcher  *Launcher
	// session replaces SSH for tests: the environment that browses an env's directories.
	session func(name string, config *chordjson.Object) RemoteSession

	// background ends with Close: the connection starts of Warm.
	background context.Context
	stop       context.CancelFunc

	mu       sync.Mutex
	tests    map[string]*chordjson.Object
	browsers map[string]*browser
}

// NewManager is the manager of directory's environments. uses counts the sessions using an environment; launcher is the
// sandbox launcher to deploy (nil: the one this package ships); session (nil: over SSH) makes the browsing environment.
func NewManager(directory string, uses func(ctx context.Context, name string) (int, error), launcher *Launcher, session func(name string, config *chordjson.Object) RemoteSession) *Manager {
	//portlint:allow ctxbackground the manager outlives every call; its background work ends with Close, which cancels this
	background, stop := context.WithCancel(context.Background())
	manager := &Manager{background: background, stop: stop, directory: directory, uses: uses, launcher: launcher, tests: map[string]*chordjson.Object{}, browsers: map[string]*browser{}}
	manager.session = session
	if session == nil {
		manager.session = func(name string, config *chordjson.Object) RemoteSession {
			return RemoteSessionFor(directory, name, config, "/", ConnectOptions{Launcher: manager.launcher})
		}
	}
	return manager
}

// Directory is the data directory the manager works in.
func (m *Manager) Directory() string { return m.directory }

// Config is environment name's configuration.
func (m *Manager) Config(ctx context.Context, name string) (*chordjson.Object, error) {
	configs, err := LoadEnvs(ctx, m.directory)
	if err != nil {
		return nil, err
	}
	config, ok := configs.Value(name).(*chordjson.Object)
	if !ok {
		return nil, fmt.Errorf(`Environment "%s" is not configured`, name)
	}
	return config, nil
}

// sshConfig is an SSH env's configuration; sandboxes have no host keys or SSH keys of their own.
func (m *Manager) sshConfig(ctx context.Context, name string) (*chordjson.Object, error) {
	config, err := m.Config(ctx, name)
	if err != nil {
		return nil, err
	}
	if config.Value("type") != "ssh" {
		return nil, fmt.Errorf(`"%s" is a sandbox; manage its host's SSH settings instead`, name)
	}
	return config, nil
}

// Connection is a pi-env connection to env name configured as config (a sandbox: through its host), with p2's key and
// known hosts, deploying the daemon pi-env ships. It starts at its first request; the caller closes it.
func (m *Manager) Connection(name string, config *chordjson.Object) *env.Connection {
	return EnvConnection(m.directory, name, config, ConnectOptions{Launcher: m.launcher}).Connection
}

// List is every environment, in name order, with the last test of its present configuration.
func (m *Manager) List(ctx context.Context) ([]any, error) {
	configs, err := LoadEnvs(ctx, m.directory)
	if err != nil {
		return nil, err
	}
	names := configs.Keys()
	slices.SortStableFunc(names, p2runtime.LocaleCompare)
	rows := make([]any, 0, len(names))
	for _, name := range names {
		config := configs.Value(name).(*chordjson.Object)
		count, err := m.uses(ctx, name)
		if err != nil {
			return nil, err
		}
		row := chordjson.ObjectOf("name", name, "config", config, "sessions", float64(count))
		m.mu.Lock()
		test := m.tests[name+"@"+EnvRevision(config)]
		m.mu.Unlock()
		if test != nil {
			row.Set("test", test)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func errorMessage(err error) string { return err.Error() }

// Save adds or replaces an environment. Other changes of a sandbox apply when its VM restarts; moving it to another host
// stops the VM on the old one first (nothing could reach it afterwards), refusing the change when that fails.
func (m *Manager) Save(ctx context.Context, name string, config any) error {
	configs, err := LoadEnvs(ctx, m.directory)
	if err != nil {
		return err
	}
	if previous, ok := configs.Value(name).(*chordjson.Object); ok && previous.Value("type") == "gondolin" {
		next, _ := config.(*chordjson.Object)
		if next == nil || next.Value("type") != "gondolin" || next.Value("host") != previous.Value("host") {
			if _, err := m.SandboxStop(ctx, name); err != nil {
				return fmt.Errorf(`Could not stop sandbox %s's VM on "%s", so it was not moved: %s`, name, previous.Value("host"), errorMessage(err))
			}
		}
	}
	if err := SaveEnv(ctx, m.directory, name, config); err != nil {
		return err
	}
	m.release(name)
	return nil
}

// Delete removes an environment. Refused while any session, archived or not, references the env, unless force: sessions
// never fall back to local tools, so theirs refuse to run until they move. A sandbox's VM is stopped and its run directory
// removed first (nothing could stop it once its configuration is gone); deleting is refused when that fails.
func (m *Manager) Delete(ctx context.Context, name string, force bool) error {
	count, err := m.uses(ctx, name)
	if err != nil {
		return err
	}
	if count > 0 && !force {
		plural := "s"
		if count == 1 {
			plural = ""
		}
		return fmt.Errorf(`%d session%s still use "%s"`, count, plural, name)
	}
	configs, err := LoadEnvs(ctx, m.directory)
	if err != nil {
		return err
	}
	if config, ok := configs.Value(name).(*chordjson.Object); ok && config.Value("type") == "gondolin" {
		if _, err := m.sandboxCommand(ctx, name, "forget"); err != nil {
			return fmt.Errorf("Could not stop sandbox %s's VM, so it was not deleted: %s", name, errorMessage(err))
		}
	}
	if err := SaveEnv(ctx, m.directory, name, nil); err != nil {
		return err
	}
	m.release(name)
	if err := DeleteSecret(ctx, m.directory, name, nil); err != nil {
		return err
	}
	// A key p2 generated or stored for this env goes with it.
	key, err := m.KeyPath(name)
	if err != nil {
		return err
	}
	_ = os.Remove(key)
	_ = os.Remove(key + ".pub")
	return nil
}

// KeyPath is where p2 keeps an env's private key, in the private data directory. Keyed by name, so a key can be made while
// the env is still being created.
func (m *Manager) KeyPath(name string) (string, error) {
	if !envName.MatchString(name) {
		return "", errors.New("Invalid environment name")
	}
	return filepath.Join(m.directory, "ssh", "keys", name), nil
}

// PublicKey is the public key of the env's managed private key, if p2 keeps one ("" if not).
func (m *Manager) PublicKey(name string) (string, error) {
	path, err := m.KeyPath(name)
	if err != nil {
		return "", err
	}
	text, err := os.ReadFile(path + ".pub")
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(text)), nil
}

// adoptKey makes a saved env use path as its identity file from now on; an env being created gets it from its form.
func (m *Manager) adoptKey(ctx context.Context, name, path string) error {
	configs, err := LoadEnvs(ctx, m.directory)
	if err != nil {
		return err
	}
	if config, ok := configs.Value(name).(*chordjson.Object); ok && config.Value("type") == "ssh" && config.Value("identityFile") != path {
		next := config.Clone()
		next.Set("identityFile", path)
		return m.Save(ctx, name, next)
	}
	return nil
}

// GenerateKey makes a new ed25519 key for the env, kept in p2's data directory and used as its identity file; it returns the
// public key to install on the host. An existing managed key is kept, so generating twice never locks you out.
func (m *Manager) GenerateKey(ctx context.Context, name string) (string, error) {
	path, err := m.KeyPath(name)
	if err != nil {
		return "", err
	}
	if existing, _ := m.PublicKey(name); existing == "" {
		if err := os.MkdirAll(filepath.Join(m.directory, "ssh", "keys"), 0o700); err != nil {
			return "", err
		}
		_ = os.Remove(path)
		if output, err := exec.CommandContext(ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "p2-"+name, "-f", path).CombinedOutput(); err != nil {
			return "", fmt.Errorf("Command failed: ssh-keygen: %s", strings.TrimSpace(string(output)))
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return "", err
		}
	}
	if err := m.adoptKey(ctx, name, path); err != nil {
		return "", err
	}
	publicKey, _ := m.PublicKey(name)
	if publicKey == "" {
		return "", errors.New("Could not generate a key")
	}
	return publicKey, nil
}

// UploadKey keeps an existing private key (OpenSSH or PEM text) for the env and uses it as its identity file; it returns its
// public key. Keys with a passphrase are refused: nothing could type it when a session connects.
func (m *Manager) UploadKey(ctx context.Context, name, key string) (string, error) {
	path, err := m.KeyPath(name)
	if err != nil {
		return "", err
	}
	if len(key) > maxKeyBytes || !strings.Contains(key, "PRIVATE KEY") {
		return "", errors.New("Not a private key")
	}
	directory := filepath.Join(m.directory, "ssh", "keys")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	temporary := filepath.Join(directory, ".upload-"+hex.EncodeToString(random[:]))
	defer func() { _ = os.Remove(temporary) }()
	if !strings.HasSuffix(key, "\n") {
		key += "\n"
	}
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(key); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	// An empty passphrase: a protected key fails here instead of when a session connects.
	output, err := exec.CommandContext(ctx, "ssh-keygen", "-y", "-P", "", "-f", temporary).Output()
	if err != nil {
		return "", errors.New("The key is protected by a passphrase or is not a valid private key")
	}
	publicKey := strings.TrimSpace(string(output))
	if err := os.Rename(temporary, path); err != nil {
		return "", err
	}
	if err := os.WriteFile(path+".pub", []byte(publicKey+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := m.adoptKey(ctx, name, path); err != nil {
		return "", err
	}
	return publicKey, nil
}

// Test connects as a session would (detect, deploy, start; a sandbox: start its VM), then measures what the Envs panel
// shows. An SSH env's test also reports whether it can host sandboxes.
func (m *Manager) Test(ctx context.Context, name string) (*chordjson.Object, error) {
	config, err := m.Config(ctx, name)
	if err != nil {
		return nil, err
	}
	var result *chordjson.Object
	if config.Value("type") == "ssh" {
		result = m.testSSH(ctx, name, config)
	} else {
		result = m.testSandbox(ctx, name, config)
	}
	m.mu.Lock()
	m.tests[name+"@"+EnvRevision(config)] = result
	m.mu.Unlock()
	return result, nil
}

func failedTest(err error) *chordjson.Object {
	return chordjson.ObjectOf("ok", false, "testedAt", float64(time.Now().UnixMilli()), "error", errorMessage(err))
}

// measure measures a started connection: its home, round trip and watch mode.
func (m *Manager) measure(ctx context.Context, name string, config *chordjson.Object, connection *env.Connection, platform, arch string, warnings []string) (*chordjson.Object, error) {
	info, err := connection.Info(ctx)
	if err != nil {
		return nil, err
	}
	remote := env.NewRemoteExecutionEnv(env.RemoteExecutionEnvOptions{Connection: connection, ID: EnvID(name, config), Cwd: info.Home})
	started := time.Now()
	if _, err := remote.FileInfo(ctx, info.Home); err != nil {
		return nil, err
	}
	latency := time.Since(started).Seconds() * 1000
	watcher, err := remote.Watch(ctx, []durableenv.WatchTarget{{Path: info.Home}}, func(durableenv.FileChange) {})
	if err != nil {
		return nil, err
	}
	mode := watcher.Mode()
	if err := watcher.Stop(ctx); err != nil {
		return nil, err
	}
	notes := make([]any, len(warnings))
	for at, warning := range warnings {
		notes[at] = warning
	}
	return chordjson.ObjectOf(
		"ok", true, "testedAt", float64(time.Now().UnixMilli()), "platform", platform, "arch", arch, "home", info.Home,
		"version", info.Version, "latencyMs", jsnumber.Round(latency), "watchMode", string(mode), "warnings", notes,
	), nil
}

func (m *Manager) testSandbox(ctx context.Context, name string, config *chordjson.Object) *chordjson.Object {
	connection := EnvConnection(m.directory, name, config, ConnectOptions{Launcher: m.launcher, OnLog: func(string) {}}).Connection
	defer connection.Close()
	info, err := connection.Info(ctx)
	if err != nil {
		return failedTest(err)
	}
	result, err := m.measure(ctx, name, config, connection, "linux", env.NodePlatform(info).Arch, nil)
	if err != nil {
		return failedTest(err)
	}
	return result
}

func (m *Manager) testSSH(ctx context.Context, name string, config *chordjson.Object) *chordjson.Object {
	options := SSHOptions(m.directory, name, config)
	options.OnLog = func(string) {}
	var result *chordjson.Object
	connection, remote, err := env.ConnectSsh(ctx, options)
	if err == nil {
		defer connection.Close()
		result, err = m.measure(ctx, name, config, connection, remote.Platform, remote.Arch, remote.Warnings)
		if err == nil {
			result.Set("sandboxes", m.sandboxSupport(ctx, options.SshTarget, remote))
		}
	}
	if err == nil {
		return result
	}
	result = failedTest(err)
	var unknown *env.HostKeyUnknownError
	var changed *env.HostKeyChangedError
	isUnknown, isChanged := errors.As(err, &unknown), errors.As(err, &changed)
	if isUnknown || isChanged {
		// Show what the host presents now; accepting it is the owner's decision.
		lines, fingerprints, scanErr := env.ScanHostKey(ctx, options.SshTarget)
		if scanErr != nil {
			lines, fingerprints = nil, nil
		}
		state := "changed"
		if isUnknown {
			state = "unknown"
		}
		result.Set("hostKey", chordjson.ObjectOf("state", state, "fingerprints", anyStrings(fingerprints), "lines", anyStrings(lines)))
	}
	return result
}

func anyStrings(values []string) []any {
	out := make([]any, len(values))
	for at, value := range values {
		out[at] = value
	}
	return out
}

func (m *Manager) getLauncher() (*Launcher, error) {
	if m.launcher != nil {
		return m.launcher, nil
	}
	return DefaultLauncher()
}

// sandboxTarget is the host of sandbox name and the system it runs (Linux, or the error saying otherwise).
func (m *Manager) sandboxTarget(ctx context.Context, name string) (config *chordjson.Object, target env.SshTarget, remote env.RemotePlatform, err error) {
	if config, err = m.Config(ctx, name); err != nil {
		return
	}
	if config.Value("type") != "gondolin" {
		err = fmt.Errorf(`"%s" is not a sandbox`, name)
		return
	}
	hostName, _ := config.Value("host").(string)
	var options env.SshConnectOptions
	if options, err = SandboxHost(ctx, m.directory, name, hostName); err != nil {
		return
	}
	target = options.SshTarget
	if remote, err = env.DetectPlatform(ctx, target); err != nil {
		return
	}
	if remote.Platform != "linux" {
		err = fmt.Errorf(`Sandboxes need a Linux host; "%s" runs %s`, hostName, remote.Platform)
	}
	return
}

// sandboxCommand runs a launcher command for sandbox name on its host and reads the VM's state from its answer.
func (m *Manager) sandboxCommand(ctx context.Context, name, command string) (*chordjson.Object, error) {
	config, target, remote, err := m.sandboxTarget(ctx, name)
	if err != nil {
		return nil, err
	}
	launcher, err := m.getLauncher()
	if err != nil {
		return nil, err
	}
	key := SandboxKey(m.directory, name)
	meta, values, err := SandboxSecrets(ctx, m.directory, name)
	if err != nil {
		return nil, err
	}
	args := []string{command, key}
	input := ""
	if command == "start" {
		args = append(args, SandboxArgument(config, meta))
		encoded, err := chordjson.Marshal(values)
		if err != nil {
			return nil, err
		}
		input = string(encoded)
	}
	report, err := LauncherCommand(ctx, target, remote, launcher, args, input)
	if err != nil {
		return nil, err
	}
	// The host's answer is wire input.
	if ok, _ := statusReport.Check(report); !ok {
		return nil, errors.New("Unexpected answer from the sandbox launcher")
	}
	answer := report.(*chordjson.Object)
	if answer.Value("state") == "stopped" {
		return chordjson.ObjectOf("state", "stopped"), nil
	}
	return chordjson.ObjectOf(
		"state", "running", "startedAt", answer.Value("startedAt"), "cpus", answer.Value("cpus"), "memoryMb", answer.Value("memoryMb"),
		"current", answer.Value("revision") == SandboxRevision(config, meta),
	), nil
}

// SandboxStatus is a sandbox's VM state.
func (m *Manager) SandboxStatus(ctx context.Context, name string) (*chordjson.Object, error) {
	return m.sandboxCommand(ctx, name, "status")
}

// SandboxStart starts a sandbox's VM with its current configuration unless it runs.
func (m *Manager) SandboxStart(ctx context.Context, name string) (*chordjson.Object, error) {
	return m.sandboxCommand(ctx, name, "start")
}

// SandboxStop stops the VM; the browsing connection goes with it.
func (m *Manager) SandboxStop(ctx context.Context, name string) (*chordjson.Object, error) {
	m.release(name)
	return m.sandboxCommand(ctx, name, "stop")
}

// SandboxResetDisk stops the VM and removes its disk: the next start begins from the guest image again.
func (m *Manager) SandboxResetDisk(ctx context.Context, name string) (*chordjson.Object, error) {
	m.release(name)
	return m.sandboxCommand(ctx, name, "reset")
}

// Secrets are sandbox name's secrets, without values.
func (m *Manager) Secrets(ctx context.Context, name string) ([]SecretInfo, error) {
	config, err := m.Config(ctx, name)
	if err != nil {
		return nil, err
	}
	if config.Value("type") != "gondolin" {
		return nil, fmt.Errorf(`"%s" is not a sandbox`, name)
	}
	return ListSecrets(m.directory, name)
}

// SetSecret sets a secret of sandbox name; addHosts first adds its hosts the sandbox does not allow yet. It applies when the
// VM starts next (the configuration's revision changes).
func (m *Manager) SetSecret(ctx context.Context, name, secretName string, change SecretChange, addHosts bool) error {
	config, err := m.Config(ctx, name)
	if err != nil {
		return err
	}
	if config.Value("type") != "gondolin" {
		return fmt.Errorf(`"%s" is not a sandbox`, name)
	}
	allowed := stringsOf(config.Value("allowHosts"))
	var missing []string
	for _, host := range change.Hosts {
		if !slices.Contains(allowed, host) {
			missing = append(missing, host)
		}
	}
	if len(missing) > 0 && addHosts {
		next := config.Clone()
		allowed = append(slices.Clone(allowed), missing...)
		next.Set("allowHosts", anyStrings(allowed))
		if err := m.Save(ctx, name, next); err != nil {
			return err
		}
	}
	return SaveSecret(ctx, m.directory, name, secretName, change, allowed)
}

// DeleteSecret removes one secret of sandbox name.
func (m *Manager) DeleteSecret(ctx context.Context, name, secret string) error {
	return DeleteSecret(ctx, m.directory, name, &secret)
}

// AcceptHostKey trusts the host key lines of SSH env name.
func (m *Manager) AcceptHostKey(ctx context.Context, name string, lines []string) error {
	config, err := m.sshConfig(ctx, name)
	if err != nil {
		return err
	}
	if err := env.AcceptHostKey(SSHOptions(m.directory, name, config).SshTarget, lines); err != nil {
		return err
	}
	m.release(name)
	return nil
}

// ForgetHostKey forgets the trusted host key of SSH env name.
func (m *Manager) ForgetHostKey(ctx context.Context, name string) error {
	config, err := m.sshConfig(ctx, name)
	if err != nil {
		return err
	}
	if err := env.ForgetHostKey(SSHOptions(m.directory, name, config).SshTarget); err != nil {
		return err
	}
	m.release(name)
	return nil
}

// browser is a connection for browsing an env's directories, shared and closed after a while without use.
func (m *Manager) browserFor(ctx context.Context, name string) (RemoteSession, error) {
	configs, err := LoadEnvs(ctx, m.directory)
	if err != nil {
		return RemoteSession{}, err
	}
	config, err := m.Config(ctx, name)
	if err != nil {
		return RemoteSession{}, err
	}
	// A sandbox's connection goes through its host: an edit of either starts a new one.
	revision := ConnectionRevision(configs, name)
	m.mu.Lock()
	current := m.browsers[name]
	var replaced *browser
	if current == nil || current.revision != revision {
		replaced = m.dropLocked(name)
		current = &browser{revision: revision, session: m.session(name, config)}
		m.browsers[name] = current
	}
	if current.timer != nil {
		current.timer.Stop()
	}
	current.timer = time.AfterFunc(idleAfter, func() {
		m.mu.Lock()
		if m.browsers[name] != current {
			m.mu.Unlock()
			return
		}
		delete(m.browsers, name)
		m.mu.Unlock()
		current.session.Connection.Close()
	})
	m.mu.Unlock()
	replaced.close()
	return current.session, nil
}

func (m *Manager) release(name string) {
	m.mu.Lock()
	dropped := m.dropLocked(name)
	m.mu.Unlock()
	dropped.close()
}

// dropLocked takes name's browsing connection out of the manager; the caller closes it once it holds no lock.
func (m *Manager) dropLocked(name string) *browser {
	current := m.browsers[name]
	if current == nil {
		return nil
	}
	delete(m.browsers, name)
	if current.timer != nil {
		current.timer.Stop()
	}
	return current
}

func (b *browser) close() {
	if b != nil {
		b.session.Connection.Close()
	}
}

// resolve is an absolute path on the env: `~` is the remote home, a relative path is relative to it.
func (m *Manager) resolve(ctx context.Context, session RemoteSession, path string) (string, error) {
	info, err := session.Connection.Info(ctx)
	if err != nil {
		return "", err
	}
	windows := info.OS == "windows"
	isAbsolute, join := nodepath.PosixIsAbsolute, nodepath.PosixJoin
	if windows {
		isAbsolute, join = nodepath.Win32IsAbsolute, nodepath.Win32Join
	}
	absolute := path
	if !(path == "~" || strings.HasPrefix(path, "~/") || isAbsolute(path)) {
		absolute = join(info.Home, path)
	}
	return session.Env.AbsolutePath(ctx, absolute)
}

// Warm starts the connections of every SSH env in the background, so the first listing does not wait for ssh. Sandboxes are
// left alone: connecting to one starts its VM.
func (m *Manager) Warm(ctx context.Context) error {
	configs, err := LoadEnvs(ctx, m.directory)
	if err != nil {
		return err
	}
	for name, each := range configs.All() {
		if each.(*chordjson.Object).Value("type") != "ssh" {
			continue
		}
		session, err := m.browserFor(ctx, name)
		if err != nil {
			continue
		}
		// In the background, as Pi's `void ....catch(() => {})`: the answer is not used, only the connection it starts.
		//portlint:allow gorecover the call's only effect is the connection it starts, and its error is dropped as Pi's catch does
		go func() { _, _ = session.Connection.Info(m.background) }()
	}
	return nil
}

// ListDirectory is the subdirectories of path on the env, following symbolic links to directories, as for local
// directories. Two round trips: opening fails for anything but a directory, one page holds most folders, and the close is
// not waited for.
func (m *Manager) ListDirectory(ctx context.Context, name, path string) (p2runtime.DirectoryListing, error) {
	session, err := m.browserFor(ctx, name)
	if err != nil {
		return p2runtime.DirectoryListing{}, err
	}
	info, err := session.Connection.Info(ctx)
	if err != nil {
		return p2runtime.DirectoryListing{}, err
	}
	resolved, err := m.resolve(ctx, session, path)
	if err != nil {
		return p2runtime.DirectoryListing{}, err
	}
	reader, err := session.Env.OpenDirReader(ctx, resolved)
	if err != nil {
		var fileErr *durableenv.FileError
		if errors.As(err, &fileErr) && fileErr.Code == durableenv.FileErrorNotDirectory {
			return p2runtime.DirectoryListing{}, errors.New("Working directory is not a directory")
		}
		return p2runtime.DirectoryListing{}, err
	}
	var entries []durableenv.FileInfo
	complete := false
	func() {
		defer func() { go func() { _ = reader.Close(context.WithoutCancel(ctx)) }() }()
		for len(entries) < listingScanLimit {
			var page durableenv.DirPage
			if page, err = reader.Next(ctx, 1000); err != nil {
				return
			}
			entries = append(entries, page.Entries...)
			if page.Done {
				complete = true
				return
			}
		}
	}()
	if err != nil {
		return p2runtime.DirectoryListing{}, err
	}
	found := make([]string, len(entries))
	var wg sync.WaitGroup
	for at, entry := range entries {
		switch entry.Kind {
		case durableenv.FileKindDirectory:
			found[at] = entry.Path
		case durableenv.FileKindSymlink:
			// fileInfo does not follow links (lstat): resolve first. All links resolve in parallel.
			wg.Add(1)
			//portlint:allow gorecover joined by wg.Wait below; a panic of the connection layer ends the call as it ends any call
			go func() {
				defer wg.Done()
				target, err := session.Env.CanonicalPath(ctx, entry.Path)
				if err != nil {
					return
				}
				if followed, err := session.Env.FileInfo(ctx, target); err == nil && followed.Kind == durableenv.FileKindDirectory {
					found[at] = entry.Path
				}
			}()
		}
	}
	wg.Wait()
	directories := make([]string, 0, len(found))
	for _, each := range found {
		if each != "" {
			directories = append(directories, each)
		}
	}
	slices.SortStableFunc(directories, p2runtime.LocaleCompare)
	dirname := nodepath.PosixDirname
	if info.OS == "windows" {
		dirname = nodepath.Win32Dirname
	}
	var parent *string
	if up := dirname(resolved); up != resolved {
		parent = &up
	}
	return p2runtime.DirectoryListing{
		Path: resolved, Parent: parent, Directories: directories[:min(len(directories), listingShown)],
		Truncated: !complete || len(directories) > listingShown,
	}, nil
}

// WorkingDirectory is a session's working directory on the env: resolved, optionally created, and checked to be a directory.
func (m *Manager) WorkingDirectory(ctx context.Context, name, path string, create bool) (string, error) {
	session, err := m.browserFor(ctx, name)
	if err != nil {
		return "", err
	}
	resolved, err := m.resolve(ctx, session, path)
	if err != nil {
		return "", err
	}
	recursive := true
	if create {
		if err := session.Env.CreateDir(ctx, resolved, &durableenv.CreateDirOptions{Recursive: &recursive}); err != nil {
			return "", err
		}
	}
	canonical, err := session.Env.CanonicalPath(ctx, resolved)
	if err != nil {
		return "", err
	}
	info, err := session.Env.FileInfo(ctx, canonical)
	if err != nil {
		return "", err
	}
	if info.Kind != durableenv.FileKindDirectory {
		return "", errors.New("Working directory is not a directory")
	}
	return resolved, nil
}

// Close ends every browsing connection.
func (m *Manager) Close() {
	m.stop()
	m.mu.Lock()
	names := make([]string, 0, len(m.browsers))
	for name := range m.browsers {
		names = append(names, name)
	}
	var sessions []RemoteSession
	for _, name := range names {
		current := m.browsers[name]
		delete(m.browsers, name)
		if current.timer != nil {
			current.timer.Stop()
		}
		sessions = append(sessions, current.session)
	}
	m.mu.Unlock()
	for _, session := range sessions {
		session.Connection.Close()
	}
}

// sandboxSupport is whether the host can run sandboxes, from the launcher's `check`; a failed check is a "none" with its
// error.
func (m *Manager) sandboxSupport(ctx context.Context, target env.SshTarget, remote env.RemotePlatform) *chordjson.Object {
	none := func(detail string) *chordjson.Object { return chordjson.ObjectOf("runtime", "none", "detail", detail) }
	if remote.Platform != "linux" {
		return none(fmt.Sprintf("Sandboxes need Linux; this host runs %s.", remote.Platform))
	}
	launcher, err := m.getLauncher()
	if err != nil {
		return none("The sandbox check failed: " + errorMessage(err))
	}
	report, err := LauncherCommand(ctx, target, remote, launcher, []string{"check"}, "")
	if err != nil {
		return none("The sandbox check failed: " + errorMessage(err))
	}
	// The host's answer is wire input.
	if ok, _ := checkReport.Check(report); !ok {
		return none("The sandbox check answered unexpectedly.")
	}
	answer := report.(*chordjson.Object)
	qemu, _ := answer.Value("qemu").(string)
	node, _ := answer.Value("node").(string)
	if answer.Value("runtime") == "native" {
		return chordjson.ObjectOf("runtime", "native", "detail", fmt.Sprintf("Gondolin runs with QEMU %s, Node %s and KVM.", qemu, node))
	}
	// `const [major = 0, minor = 0] = qemu.split(".").map(Number)`: a part that is no number is NaN, which fails every
	// comparison below.
	parts := strings.Split(qemu, ".")
	major, minor := jsnumber.Parse(parts[0]), 0.0
	if len(parts) > 1 {
		minor = jsnumber.Parse(parts[1])
	}
	var missing []string
	if answer.Value("kvm") != true {
		if answer.Value("kvmDevice") == true {
			missing = append(missing, "/dev/kvm is not accessible to this user")
		} else {
			missing = append(missing, "there is no /dev/kvm")
		}
	}
	if qemu == "" {
		missing = append(missing, "QEMU is not installed (on the PATH or in ~/.local/bin)")
	} else if major < 7 || (major == 7 && minor < 2) {
		missing = append(missing, fmt.Sprintf("QEMU %s is too old (Gondolin needs 7.2)", qemu))
	}
	if node == "" || jsnumber.Parse(strings.Split(node, ".")[0]) < 22 {
		missing = append(missing, "Node 22 or newer is not installed (on the PATH or in ~/.local/bin)")
	}
	return none(fmt.Sprintf("This host cannot run sandboxes: %s.", strings.Join(missing, ", ")))
}

var _ = delta.NewJsonObject
var _ = contract.DefaultDiskGB
