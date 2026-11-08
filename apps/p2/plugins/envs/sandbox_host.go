package envs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// p2's sandbox launcher on a host environment (apps/p2/plugins/envs/sandbox-host.ts): deploying it (the files of sandbox/,
// byte for byte Pi's, named by their content under the host's ~/.pi/p2/sandbox/), the command line that runs it over SSH,
// and one-shot commands (check, start, status, stop) whose JSON answer p2 reads.

//go:embed sandbox
var launcherFiles embed.FS

// DefaultDiskGB is the disk size of a sandbox without diskGb (DEFAULT_DISK_GB of schema.ts).
const DefaultDiskGB = 32

// launcherSources are the launcher's files as deployed: name on the host, file in the launcher's source directory.
var launcherSources = [...][2]string{
	{"p2-sandbox.sh", "p2-sandbox.sh"},
	{"p2-sandbox.mjs", "p2-sandbox.mjs"},
	{"package.json", "host-package.json"},
	{"package-lock.json", "host-package-lock.json"},
}

type launcherFile struct {
	name    string
	content []byte
}

// Launcher is the launcher's files and the name of their directory on a host (their SHA-256).
type Launcher struct {
	files []launcherFile
	// ID is the first 16 hex digits of the SHA-256 of the files, each as `name\0length\0content`.
	ID string
}

// LoadLauncher reads the launcher from source (the sandbox/ directory of the plugin).
func LoadLauncher(source fs.FS) (*Launcher, error) {
	launcher := &Launcher{}
	hash := sha256.New()
	for _, each := range launcherSources {
		content, err := fs.ReadFile(source, each[1])
		if err != nil {
			return nil, err
		}
		launcher.files = append(launcher.files, launcherFile{each[0], content})
		fmt.Fprintf(hash, "%s\x00%d\x00", each[0], len(content))
		hash.Write(content)
	}
	launcher.ID = hex.EncodeToString(hash.Sum(nil))[:16]
	return launcher, nil
}

var defaultLauncher = sync.OnceValues(func() (*Launcher, error) {
	source, err := fs.Sub(launcherFiles, "sandbox")
	if err != nil {
		return nil, err
	}
	return LoadLauncher(source)
})

// DefaultLauncher is the launcher this package ships, read once.
func DefaultLauncher() (*Launcher, error) { return defaultLauncher() }

const (
	// commandTimeout is how long a deployment or one-shot command may take; `start` may download the guest image first.
	commandTimeout = 60 * time.Second
	startTimeout   = 660 * time.Second
)

func quote(text string) string { return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'" }

// SecretMeta as the launcher's argument names it, with Pi's key order.
func (secret SecretMeta) object() *chordjson.Object {
	toAny := func(list []string) []any {
		out := make([]any, len(list))
		for at, item := range list {
			out[at] = item
		}
		return out
	}
	object := chordjson.ObjectOf("name", secret.Name, "hosts", toAny(secret.Hosts), "version", secret.Version)
	if secret.Aliases != nil {
		object.Set("aliases", toAny(secret.Aliases))
	}
	if secret.GitHosts != nil {
		object.Set("gitHosts", toAny(secret.GitHosts))
	}
	return object
}

// SandboxArgument is the sandbox configuration the launcher takes: base64 JSON of what the VM needs. Secrets are named with
// their hosts and versions, never their values (the command line is visible on the host): `start` reads those on stdin.
func SandboxArgument(config *chordjson.Object, secrets []SecretMeta) string {
	value := chordjson.NewObject(7)
	if image, ok := config.Get("image"); ok && image != nil {
		value.Set("image", image)
	}
	list := func(key string) any {
		if items, ok := config.Value(key).([]any); ok {
			return items
		}
		return []any{}
	}
	value.Set("mounts", list("mounts"))
	value.Set("allowHosts", list("allowHosts"))
	if cpus, ok := config.Get("cpus"); ok && cpus != nil {
		value.Set("cpus", cpus)
	}
	if memory, ok := config.Get("memoryMb"); ok && memory != nil {
		value.Set("memoryMb", memory)
	}
	if disk, ok := config.Get("diskGb"); ok && disk != nil {
		value.Set("diskGb", disk)
	} else {
		value.Set("diskGb", float64(DefaultDiskGB))
	}
	if len(secrets) > 0 {
		metas := make([]any, len(secrets))
		for at, secret := range secrets {
			metas[at] = secret.object()
		}
		value.Set("secrets", metas)
	}
	text, err := chordjson.Marshal(value)
	if err != nil {
		panic(err) // a validated configuration is strict JSON
	}
	return base64.StdEncoding.EncodeToString(text)
}

// SandboxRevision is the configuration's revision as the launcher reports it for the running VM (it applies at the next
// start).
func SandboxRevision(config *chordjson.Object, secrets []SecretMeta) string {
	sum := sha256.Sum256([]byte(SandboxArgument(config, secrets)))
	return hex.EncodeToString(sum[:])[:16]
}

// RunLauncher is the remote command line that runs the launcher in directory with args.
func RunLauncher(directory string, args []string) string {
	parts := make([]string, 0, len(args)+2)
	parts = append(parts, "sh", quote(directory+"/p2-sandbox.sh"))
	for _, arg := range args {
		parts = append(parts, quote(arg))
	}
	return strings.Join(parts, " ")
}

func sshProgram(target env.SshTarget) string {
	if target.Ssh != nil {
		return *target.Ssh
	}
	return "ssh"
}

// runOnHost runs command on the host over SSH, optionally feeding input; it returns stdout, or stderr as the error.
func runOnHost(ctx context.Context, target env.SshTarget, command, input string, timeout time.Duration) (string, error) {
	arguments, err := env.SshArguments(target)
	if err != nil {
		return "", err
	}
	//nolint:gosec // G204 (class c): program is the ssh option of the user's SSH target (default ssh); the arguments come from env.SshArguments, which validates them, then the launcher command PiG builds.
	cmd := exec.CommandContext(ctx, sshProgram(target), append(arguments, command)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdin = strings.NewReader(input)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return "", err
	}
	timer := time.AfterFunc(timeout, func() { _ = cmd.Process.Kill() })
	err = cmd.Wait()
	timer.Stop()
	if err == nil {
		return stdout.String(), nil
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if message := jsstring.Trim(stderr.String()); message != "" {
		return "", errors.New(message)
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() >= 0 {
		return "", fmt.Errorf("ssh exited with code %d", exit.ExitCode())
	}
	return "", errors.New("ssh exited with code null")
}

func hashHex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// deployScript is the script that makes sure the launcher is in directory under root on the host.
func (launcher *Launcher) deployScript(root string) (directory, script string) {
	directory = root + "/" + launcher.ID
	lines := []string{
		"set -e",
		"d=" + quote(directory),
		"r=" + quote(root),
		`hash() { sha256sum "$1" 2>/dev/null | cut -d" " -f1; }`,
		"intact() {",
		`	[ -f "$d/.complete" ] || return 1`,
	}
	for _, file := range launcher.files {
		lines = append(lines, fmt.Sprintf(`	[ "$(hash "$d/%s")" = %s ] || return 1`, file.name, hashHex(file.content)))
	}
	lines = append(lines,
		"}",
		"if ! intact; then",
		`	mkdir -p "$r"`,
		`	t=$(mktemp -d "$d.XXXXXX")`,
	)
	for _, file := range launcher.files {
		lines = append(lines, fmt.Sprintf(`	printf %%s %s | base64 -d > "$t/%s"`, quote(base64.StdEncoding.EncodeToString(file.content)), file.name))
	}
	lines = append(lines,
		`	chmod 755 "$t/p2-sandbox.sh"`,
		`	touch "$t/.complete"`,
		`	rm -rf "$d"`,
		`	mv "$t" "$d"`,
		"fi",
		`touch "$d"`,
		`find "$r" -mindepth 1 -maxdepth 1 -type d -name "????????????????" -mtime +30 -exec rm -rf {} +`,
	)
	return directory, strings.Join(lines, "\n")
}

// DeployLauncher makes sure this launcher is on the host and returns its directory. One round trip: the script checks the
// files' hashes (a changed file is written again) and writes a missing or changed directory through a temporary one renamed
// into place. Other versions stay (other p2 installations may use them) until unused for 30 days.
func DeployLauncher(ctx context.Context, target env.SshTarget, remote env.RemotePlatform, launcher *Launcher) (string, error) {
	directory, script := launcher.deployScript(remote.Home + "/.pi/p2/sandbox")
	if _, err := runOnHost(ctx, target, "sh -s", script+"\n", commandTimeout); err != nil {
		return "", err
	}
	return directory, nil
}

// SandboxKey is the name a sandbox's VM has on its host: the sandbox's name, prefixed by this p2 installation (its machine and
// data directory), so two installations with a sandbox of the same name on one host never share a VM.
func SandboxKey(directory, name string) string {
	hostname, _ := os.Hostname()
	return hashHex([]byte(hostname + "\x00" + directory))[:8] + "." + name
}

// LauncherCommand runs a command of the launcher on the host (deploying the launcher first) and parses its last line of
// output as JSON.
func LauncherCommand(ctx context.Context, target env.SshTarget, remote env.RemotePlatform, launcher *Launcher, args []string, input string) (any, error) {
	directory, err := DeployLauncher(ctx, target, remote, launcher)
	if err != nil {
		return nil, err
	}
	return DeployedCommand(ctx, target, directory, args, input)
}

// DeployedCommand runs a command of the launcher deployed in directory and parses its last line of output as JSON; `start`
// may take minutes (a first start installs Gondolin and downloads the guest image).
func DeployedCommand(ctx context.Context, target env.SshTarget, directory string, args []string, input string) (any, error) {
	timeout := commandTimeout
	if len(args) > 0 && args[0] == "start" {
		timeout = startTimeout
	}
	output, err := runOnHost(ctx, target, RunLauncher(directory, args), input, timeout)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(jsstring.Trim(output), "\n")
	line := lines[len(lines)-1]
	value, err := chordjson.Decode([]byte(line))
	if err != nil {
		return nil, fmt.Errorf("Unexpected answer from the sandbox launcher: %s", jsstring.Slice(line, 0, 200))
	}
	return value, nil
}
