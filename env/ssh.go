package env

// Ports packages/env/src/ssh.ts

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// RemotePlatform is a remote system the package ships a daemon for.
type RemotePlatform struct {
	// Platform is linux, android, darwin or windows.
	Platform string
	// Arch is x64 or arm64.
	Arch string
	// Home is the remote home directory, in the remote system's own spelling.
	Home string
	// Shell is, on Windows, the shell sshd runs commands with (DefaultShell): cmd or powershell.
	Shell string
	// Warnings are problems worth telling the owner about, such as Termux without termux-exec.
	Warnings []string
}

// SshTarget is how to reach a machine with the system ssh.
//
// A nil User, Port, IdentityFile, Ssh or ConfigFile is unset, as `undefined` is in Pi; a set value is used as given, so
// SshArguments rejects an explicitly empty user or identity file and port 0 with Pi's messages.
type SshTarget struct {
	// Host is a host name or ~/.ssh/config alias.
	Host         string
	User         *string
	Port         *int
	IdentityFile *string
	// KnownHostsFile holds the host keys this application trusts; AcceptHostKey adds to it.
	KnownHostsFile string
	// HostKeyAlias is the name keys are stored under, independent of aliases, ports and jump hosts, e.g.
	// `pi-env-<env name>`.
	HostKeyAlias string
	// Ssh is the ssh program; default ssh.
	Ssh *string
	// ConfigFile is an ssh configuration file instead of ~/.ssh/config (-F).
	ConfigFile *string
}

// ErrorOptions is the options argument the host-key errors inherit from the Error constructor; Cause becomes their `cause`.
type ErrorOptions struct {
	Cause error
}

// HostKeyUnknownError means the remote host's key is not in KnownHostsFile; ScanHostKey shows it so the owner can
// accept it.
type HostKeyUnknownError struct {
	Message string
	// Cause is the `cause` a caller passes as the inherited Error constructor's ErrorOptions (`new HostKeyUnknownError(message, { cause })`); ssh.go's own errors leave it nil, as ssh.ts does.
	Cause error
}

// NewHostKeyUnknownError is `new HostKeyUnknownError(message, options)`: the inherited Error constructor keeps options.Cause as `cause`.
func NewHostKeyUnknownError(message string, options ...ErrorOptions) *HostKeyUnknownError {
	err := &HostKeyUnknownError{Message: message}
	if len(options) > 0 {
		err.Cause = options[0].Cause
	}
	return err
}

func (e *HostKeyUnknownError) Error() string { return e.Message }

// Unwrap returns Cause, the `cause` property.
func (e *HostKeyUnknownError) Unwrap() error { return e.Cause }

// Name is the `name` property. Upstream's class sets none, so it is the inherited "Error".
func (e *HostKeyUnknownError) Name() string { return "Error" }

// HostKeyChangedError means the remote host's key differs from the one in KnownHostsFile; it is never accepted
// automatically.
type HostKeyChangedError struct {
	Message string
	// Cause is the `cause` a caller passes as the inherited Error constructor's ErrorOptions (`new HostKeyChangedError(message, { cause })`); ssh.go's own errors leave it nil, as ssh.ts does.
	Cause error
}

// NewHostKeyChangedError is `new HostKeyChangedError(message, options)`: the inherited Error constructor keeps options.Cause as `cause`.
func NewHostKeyChangedError(message string, options ...ErrorOptions) *HostKeyChangedError {
	err := &HostKeyChangedError{Message: message}
	if len(options) > 0 {
		err.Cause = options[0].Cause
	}
	return err
}

func (e *HostKeyChangedError) Error() string { return e.Message }

// Unwrap returns Cause, the `cause` property.
func (e *HostKeyChangedError) Unwrap() error { return e.Cause }

// Name is the `name` property. Upstream's class sets none, so it is the inherited "Error".
func (e *HostKeyChangedError) Name() string { return "Error" }

// SshError is an ssh invocation that failed for another reason, with its exit code and diagnostics.
type SshError struct {
	Message string
	// ExitCode is nil when ssh did not exit with a code (it was killed, or could not start).
	ExitCode *int
	Stderr   string
}

func (e *SshError) Error() string { return e.Message }

// Name is the `name` property. Upstream's class sets none, so it is the inherited "Error".
func (e *SshError) Name() string { return "Error" }

// NewSshError builds an SshError. Ports packages/env/src/ssh.ts SshError constructor(message, exitCode, stderr); a nil exitCode is upstream's null.
func NewSshError(message string, exitCode *int, stderr string) *SshError {
	e := &SshError{Message: message, ExitCode: exitCode, Stderr: stderr}
	return e
}

var invalidPath = regexp.MustCompile(`["\x00-\x1f\x7f]`)

// invalidFieldRune is JavaScript's /[\s\x00-\x1f\x7f]/: \s there includes Unicode spaces such as U+00A0 and U+FEFF.
func invalidFieldRune(r rune) bool { return r <= 0x1f || r == 0x7f || jsstring.IsSpace(r) }

// checkField refuses a leading `-` (it would be read as an ssh option), whitespace and control characters.
func checkField(name, value string) error {
	if value == "" || strings.HasPrefix(value, "-") || strings.ContainsFunc(value, invalidFieldRune) {
		return fmt.Errorf("Invalid %s: %s", name, jsonString(value))
	}
	return nil
}

// configPath is a path for an ssh option that expands `%` tokens: quoted, with `%` literal.
func configPath(name, path string) (string, error) {
	if path == "" || invalidPath.MatchString(path) {
		return "", fmt.Errorf("Invalid %s: %s", name, jsonString(path))
	}
	return `"` + strings.ReplaceAll(path, "%", "%%") + `"`, nil
}

// SshArgumentsOptions are the optional trailing parameters of [SshArguments]: whether host keys are checked strictly, and the known hosts file.
type SshArgumentsOptions struct {
	StrictHostKeys *bool
	KnownHostsFile *string
}

// SshArguments are the arguments for ssh up to the host: no prompts, no forwarding of any kind, no shared
// connections, no commands from the configuration, no locale forwarding (the remote uses its own), and host keys
// checked strictly against the application's own file under a fixed alias.
//
// The optional options are upstream's trailing `strictHostKeys = true` and `knownHostsFile = target.knownHostsFile` parameters; a nil member is an absent argument.
func SshArguments(target SshTarget, options ...SshArgumentsOptions) ([]string, error) {
	strictHostKeys, knownHostsFile := true, target.KnownHostsFile
	if len(options) > 0 {
		if options[0].StrictHostKeys != nil {
			strictHostKeys = *options[0].StrictHostKeys
		}
		if options[0].KnownHostsFile != nil {
			knownHostsFile = *options[0].KnownHostsFile
		}
	}
	return SshArgumentsWith(target, strictHostKeys, knownHostsFile)
}

// SshArgumentsWith is `sshArguments(target, strictHostKeys, knownHostsFile)` with every parameter given, in Pi's order (ssh.ts:77-81). Pi's defaults are
// `strictHostKeys = true` and `knownHostsFile = target.knownHostsFile`; [SshArguments] applies them.
func SshArgumentsWith(target SshTarget, strictHostKeys bool, knownHostsFile string) ([]string, error) {
	if err := firstError(checkField("host", target.Host), checkField("host key alias", target.HostKeyAlias)); err != nil {
		return nil, err
	}
	if target.User != nil {
		if err := checkField("user", *target.User); err != nil {
			return nil, err
		}
	}
	if target.Port != nil && (*target.Port < 1 || *target.Port > 65535) {
		return nil, fmt.Errorf("Invalid port: %d", *target.Port)
	}
	knownHostsPath, err := configPath("known hosts file", knownHostsFile)
	if err != nil {
		return nil, err
	}
	option := func(setting string) []string { return []string{"-o", setting} }
	strict := "accept-new"
	if strictHostKeys {
		strict = "yes"
	}
	var arguments []string
	add := func(parts ...[]string) {
		for _, part := range parts {
			arguments = append(arguments, part...)
		}
	}
	if target.ConfigFile != nil {
		add([]string{"-F", *target.ConfigFile})
	}
	add(
		[]string{"-T", "-a", "-x"},
		option("BatchMode=yes"), option("ClearAllForwardings=yes"), option("ForwardAgent=no"), option("ForwardX11=no"),
		option("ControlMaster=no"), option("ControlPath=none"), option("RemoteCommand=none"), option("PermitLocalCommand=no"),
		option("SendEnv=-*"), option("ServerAliveInterval=15"), option("StrictHostKeyChecking="+strict),
		option("UserKnownHostsFile="+knownHostsPath), option("GlobalKnownHostsFile=none"), option("HashKnownHosts=no"),
		option("HostKeyAlias="+target.HostKeyAlias),
	)
	if target.User != nil {
		add([]string{"-l", *target.User})
	}
	if target.Port != nil {
		add([]string{"-p", strconv.Itoa(*target.Port)})
	}
	if target.IdentityFile != nil {
		identity, err := configPath("identity file", *target.IdentityFile)
		if err != nil {
			return nil, err
		}
		add(option("IdentityFile="+identity), option("IdentitiesOnly=yes"))
	}
	add([]string{"--", target.Host})
	return arguments, nil
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

const (
	// sshTimeout is how long a detection, check or upload over ssh may take before it is abandoned.
	sshTimeout    = 60 * time.Second
	uploadTimeout = 300 * time.Second
)

type sshRun struct {
	stdin   []byte
	args    []string
	timeout time.Duration
}

var (
	hostKeyChanged = regexp.MustCompile(`REMOTE HOST IDENTIFICATION HAS CHANGED`)
	hostKeyUnknown = regexp.MustCompile(`Host key verification failed|No .* host key is known`)
)

func sshProgram(target SshTarget) string {
	if target.Ssh != nil {
		return *target.Ssh
	}
	return "ssh"
}

// runSsh runs one remote command over ssh, optionally feeding stdin; it returns stdout, or the failure.
func runSsh(ctx context.Context, target SshTarget, command string, options sshRun) (string, error) {
	args := options.args
	if args == nil {
		var err error
		if args, err = SshArguments(target); err != nil {
			return "", err
		}
	}
	timeout := options.timeout
	if timeout == 0 {
		timeout = sshTimeout
	}
	runContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runContext, sshProgram(target), append(slices.Clone(args), command)...)
	cmd.Stdin = bytes.NewReader(options.stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	diagnostics := stderr.String()
	if err == nil {
		return stdout.String(), nil
	}
	if runContext.Err() != nil && ctx.Err() == nil {
		return "", NewSshError(fmt.Sprintf("ssh %s did not finish within %s s: %s", target.Host, formatSeconds(timeout), jsstring.Trim(diagnostics)), nil, diagnostics)
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if exitError, ok := errors.AsType[*exec.ExitError](err); ok {
		code := exitError.ExitCode()
		switch {
		case hostKeyChanged.MatchString(diagnostics):
			return "", NewHostKeyChangedError(fmt.Sprintf("The host key of %s changed; remove the old key to continue", target.Host))
		case hostKeyUnknown.MatchString(diagnostics):
			return "", NewHostKeyUnknownError(fmt.Sprintf("The host key of %s is not trusted yet", target.Host))
		}
		var exitCode *int
		message := fmt.Sprintf("ssh %s failed with exit code %d: %s", target.Host, code, jsstring.Trim(diagnostics))
		if code >= 0 {
			exitCode = &code
		} else {
			message = fmt.Sprintf("ssh %s failed with exit code null: %s", target.Host, jsstring.Trim(diagnostics))
		}
		return "", NewSshError(message, exitCode, diagnostics)
	}
	return "", NewSshError(err.Error(), nil, "")
}

func formatSeconds(duration time.Duration) string {
	return strconv.FormatFloat(duration.Seconds(), 'f', -1, 64)
}

// ScanHostKey connects once with accept-new against a temporary known-hosts file to capture the key the host
// presents, through the same route (~/.ssh/config, jump hosts) as real connections. It returns its known-hosts lines
// and fingerprints. Showing a fingerprint is not authentication: compare it with one obtained out of band before
// accepting it.
func ScanHostKey(ctx context.Context, target SshTarget) (lines, fingerprints []string, err error) {
	directory, err := os.MkdirTemp("", "pi-env-hostkey-")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	scanned := filepath.Join(directory, "known_hosts")
	args, err := SshArguments(target, SshArgumentsOptions{StrictHostKeys: new(false), KnownHostsFile: &scanned})
	if err != nil {
		return nil, nil, err
	}
	// Authentication may fail without the key; the key is recorded before authentication.
	if _, runErr := runSsh(ctx, target, "exit 0", sshRun{args: args}); runErr != nil {
		var sshErr *SshError
		var unknown *HostKeyUnknownError
		if !errors.As(runErr, &sshErr) && !errors.As(runErr, &unknown) {
			return nil, nil, runErr
		}
	}
	// A file that exists but cannot be read fails, as readFile after existsSync does.
	if content, readErr := os.ReadFile(scanned); readErr == nil {
		lines = nonBlankLines(string(content))
	} else if !errors.Is(readErr, fs.ErrNotExist) {
		return nil, nil, readErr
	}
	if len(lines) == 0 {
		return nil, nil, fmt.Errorf("No host key received from %s", target.Host)
	}
	keygen := exec.CommandContext(ctx, "ssh-keygen", "-lf", scanned)
	output, keygenErr := keygen.Output()
	if _, isExit := errors.AsType[*exec.ExitError](keygenErr); keygenErr != nil && !isExit {
		return nil, nil, keygenErr
	}
	return lines, nonBlankLines(string(output)), nil
}

func nonBlankLines(text string) []string {
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		if jsstring.Trim(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

var (
	hostKeyType = regexp.MustCompile(`^(ssh-[a-z0-9-]+|ecdsa-sha2-[a-z0-9-]+|sk-[a-z0-9@.-]+)$`)
	hostKeyData = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)
)

// splitFields is JavaScript's trim().split(/\s+/), without the empty field of a blank line.
func splitFields(line string) []string { return strings.FieldsFunc(line, jsstring.IsSpace) }

// parseHostKeyLine reads a known-hosts line for alias: `alias type key [comment]`, no markers, patterns or hashed
// names.
func parseHostKeyLine(alias, line string) (keyType, key string, err error) {
	fields := splitFields(line)
	if len(fields) < 3 || fields[0] != alias || !hostKeyType.MatchString(fields[1]) || !hostKeyData.MatchString(fields[2]) {
		return "", "", fmt.Errorf("Not a host key line for %s: %s", alias, jsonString(line))
	}
	return fields[1], fields[2], nil
}

// knownHostsLocks makes changes to one known-hosts file happen one at a time.
var knownHostsLocks sync.Map

// changeKnownHosts rewrites the known-hosts file one change at a time, atomically.
func changeKnownHosts(file string, change func(lines []string) ([]string, error)) error {
	lock, _ := knownHostsLocks.LoadOrStore(file, &sync.Mutex{})
	mutex := lock.(*sync.Mutex)
	mutex.Lock()
	defer mutex.Unlock()
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	var existing []string
	// Only a missing file is empty: one that cannot be read fails the change instead of being replaced.
	previous, err := os.ReadFile(file)
	switch {
	case err == nil:
		for line := range strings.SplitSeq(string(previous), "\n") {
			if line != "" {
				existing = append(existing, line)
			}
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	lines, err := change(existing)
	if err != nil {
		return err
	}
	var random [6]byte
	_, _ = rand.Read(random[:])
	temporary := file + "." + hex.EncodeToString(random[:]) + ".tmp"
	content := ""
	if len(lines) > 0 {
		content = strings.Join(lines, "\n") + "\n"
	}
	handle, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := handle.WriteString(content)
	if err := errors.Join(writeErr, handle.Close()); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, file); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

// AcceptHostKey trusts host-key lines from ScanHostKey by adding them to the target's known-hosts file. Only plain
// lines for the target's alias are accepted. A key that differs from a trusted key of the same type is refused with
// HostKeyChangedError; ForgetHostKey must remove the old keys first.
func AcceptHostKey(target SshTarget, lines []string) error {
	alias := target.HostKeyAlias
	type accepted struct{ line, keyType, key string }
	var parsed []accepted
	for _, line := range lines {
		keyType, key, err := parseHostKeyLine(alias, line)
		if err != nil {
			return err
		}
		parsed = append(parsed, accepted{jsstring.Trim(line), keyType, key})
	}
	return changeKnownHosts(target.KnownHostsFile, func(existing []string) ([]string, error) {
		trusted := map[string]string{}
		for _, line := range existing {
			if fields := splitFields(line); len(fields) >= 3 && fields[0] == alias {
				trusted[fields[1]] = fields[2]
			}
		}
		var added []string
		for _, item := range parsed {
			if known, ok := trusted[item.keyType]; ok {
				if known == item.key {
					continue
				}
				return nil, NewHostKeyChangedError(fmt.Sprintf("The %s host key of %s changed; forget the old key first", item.keyType, target.Host))
			}
			trusted[item.keyType] = item.key
			added = append(added, item.line)
		}
		return append(slices.Clone(existing), added...), nil
	})
}

// ForgetHostKey stops trusting every key stored for the target's alias, e.g. after the owner confirmed a changed host
// key.
func ForgetHostKey(target SshTarget) error {
	return changeKnownHosts(target.KnownHostsFile, func(existing []string) ([]string, error) {
		var kept []string
		for _, line := range existing {
			if fields := splitFields(line); len(fields) == 0 || fields[0] != target.HostKeyAlias {
				kept = append(kept, line)
			}
		}
		return kept, nil
	})
}

// posixProbe is the POSIX detection, run by the remote login shell; a Windows host answers through PowerShell instead.
const posixProbe = `sh -c 'echo PI-ENV-PROBE; uname -s; uname -m; uname -o 2>/dev/null || echo -; printf "%s\n" "$HOME" "${TMPDIR:--}" "${LD_PRELOAD:--}"'`

func powershell(script string) string {
	units := utf16.Encode([]rune(script))
	bytes := make([]byte, 0, len(units)*2)
	for _, unit := range units {
		bytes = binary.LittleEndian.AppendUint16(bytes, unit)
	}
	return "powershell -NoProfile -NonInteractive -EncodedCommand " + base64.StdEncoding.EncodeToString(bytes)
}

var windowsProbe = powershell("'PI-ENV-PROBE'; 'Windows'; [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString(); '-'; $HOME; '-'; '-'")

func normalizeArch(machine string) (string, error) {
	switch strings.ToLower(machine) {
	case "x86_64", "amd64", "x64":
		return "x64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	}
	return "", fmt.Errorf("Unsupported remote architecture: %s", machine)
}

type probe struct{ system, machine, os, home, tmpdir, preload string }

var lineBreak = regexp.MustCompile(`\r?\n`)

func parseProbe(target SshTarget, output string) (probe, error) {
	// Login shells may print a banner first.
	lines := lineBreak.Split(output, -1)
	start := slices.Index(lines, "PI-ENV-PROBE")
	if start == -1 {
		return probe{}, fmt.Errorf("Unexpected answer from %s: %s", target.Host, jsstring.Trim(output))
	}
	rest := lines[start+1:]
	field := func(index int, fallback string) string {
		if index < len(rest) {
			return rest[index]
		}
		return fallback
	}
	return probe{
		system: field(0, ""), machine: field(1, ""), os: field(2, ""), home: field(3, ""), tmpdir: field(4, "-"), preload: field(5, "-"),
	}, nil
}

// termuxWarnings: Termux works only with its own TMPDIR and with termux-exec, which makes `#!/usr/bin/env` shebangs
// work.
func termuxWarnings(p probe) []string {
	var warnings []string
	if p.tmpdir == "-" {
		warnings = append(warnings, "TMPDIR is not set; Termux's sshd normally sets it to $PREFIX/tmp.")
	}
	if !strings.Contains(p.preload, "termux-exec") {
		warnings = append(warnings, "termux-exec is not loaded (LD_PRELOAD); scripts with #!/usr/bin/env shebangs will fail.")
	}
	return append(warnings, "Android may suspend Termux; run termux-wake-lock on the device to keep the connection alive.")
}

var gitBashSystem = regexp.MustCompile(`^(MINGW|MSYS|CYGWIN)`)

func isHostKeyError(err error) bool {
	var unknown *HostKeyUnknownError
	var changed *HostKeyChangedError
	return errors.As(err, &unknown) || errors.As(err, &changed)
}

// DetectPlatform finds which system the target runs: uname through the login shell, or PowerShell on Windows.
func DetectPlatform(ctx context.Context, target SshTarget) (RemotePlatform, error) {
	var found probe
	output, err := runSsh(ctx, target, posixProbe, sshRun{})
	if err == nil {
		found, err = parseProbe(target, output)
	}
	if err != nil {
		// cmd.exe or PowerShell as the remote shell: no sh, or one that gets the probe's quotes wrong.
		if isHostKeyError(err) || ctx.Err() != nil {
			return RemotePlatform{}, err
		}
		if found, err = windowsProbeResult(ctx, target); err != nil {
			return RemotePlatform{}, err
		}
	}
	// Git Bash as Windows' default SSH shell: ask PowerShell for Windows' own architecture and home spelling.
	if gitBashSystem.MatchString(found.system) {
		if found, err = windowsProbeResult(ctx, target); err != nil {
			return RemotePlatform{}, err
		}
	}
	arch, err := normalizeArch(found.machine)
	if err != nil {
		return RemotePlatform{}, err
	}
	switch found.system {
	case "Windows":
		// cmd.exe expands %OS%; PowerShell prints it as is.
		answer, err := runSsh(ctx, target, "echo %OS%", sshRun{})
		if err != nil {
			return RemotePlatform{}, err
		}
		shell := "powershell"
		if strings.Contains(answer, "Windows_NT") {
			shell = "cmd"
		}
		return RemotePlatform{Platform: "windows", Arch: arch, Home: found.home, Shell: shell}, nil
	case "Darwin":
		return RemotePlatform{Platform: "darwin", Arch: arch, Home: found.home}, nil
	case "Linux":
		if found.os == "Android" {
			return RemotePlatform{Platform: "android", Arch: arch, Home: found.home, Warnings: termuxWarnings(found)}, nil
		}
		return RemotePlatform{Platform: "linux", Arch: arch, Home: found.home}, nil
	}
	return RemotePlatform{}, fmt.Errorf("Unsupported remote system: %s", found.system)
}

func windowsProbeResult(ctx context.Context, target SshTarget) (probe, error) {
	output, err := runSsh(ctx, target, windowsProbe, sshRun{})
	if err != nil {
		return probe{}, err
	}
	return parseProbe(target, output)
}

// pig additive (D97): the daemons are found next to the running executable, not inside an npm package.
// packagedDaemonDirectory is where the daemons built for each remote system live: `pi-env-<platform>-<arch>/pi-env`
// below it. PI_ENV_DAEMON_DIR overrides it; by default it is the `pi-env` directory next to the running executable.
func packagedDaemonDirectory() string {
	if directory := os.Getenv("PI_ENV_DAEMON_DIR"); directory != "" {
		return directory
	}
	executable, err := os.Executable()
	if err != nil {
		return "pi-env"
	}
	return filepath.Join(filepath.Dir(executable), "pi-env")
}

// PackagedDaemon is the daemon binary shipped for a remote system.
func PackagedDaemon(remote RemotePlatform) string {
	name := "pi-env"
	if remote.Platform == "windows" {
		name = "pi-env.exe"
	}
	return filepath.Join(packagedDaemonDirectory(), "pi-env-"+remote.Platform+"-"+remote.Arch, name)
}

func quotePosix(text string) string { return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'" }

func quotePowerShell(text string) string { return "'" + strings.ReplaceAll(text, "'", "''") + "'" }

// daemonPath is where a daemon with this content lives on the remote machine: named by its SHA-256, so versions never
// collide.
func daemonPath(remote RemotePlatform, digest string) string {
	name := "pi-env-" + digest[:32]
	if remote.Platform == "windows" {
		return remote.Home + `\.pi\mobile\tools\` + name + ".exe"
	}
	return remote.Home + "/.pi/mobile/tools/" + name
}

const posixHash = `hash() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | cut -d' ' -f1; elif command -v openssl >/dev/null 2>&1; then openssl dgst -sha256 "$1" | sed 's/.*= //'; else echo none; fi; }`

// checkDaemon reports whether the remote file has this content: present, missing, or (POSIX without a hash tool)
// nohash.
func checkDaemon(ctx context.Context, target SshTarget, remote RemotePlatform, file, digest string) (string, error) {
	if remote.Platform == "windows" {
		check := powershell(fmt.Sprintf("$f = %s; if ((Test-Path -LiteralPath $f) -and ((Get-FileHash -Algorithm SHA256 -LiteralPath $f).Hash.ToLower() -eq '%s')) { 'present' } else { 'missing' }", quotePowerShell(file), digest))
		answer, err := runSsh(ctx, target, check, sshRun{})
		if err != nil {
			return "", err
		}
		if strings.HasSuffix(jsstring.Trim(answer), "present") {
			return "present", nil
		}
		return "missing", nil
	}
	check := fmt.Sprintf(`%s; f=%s; if [ -f "$f" ] && [ "$(hash "$f")" = %s ]; then echo present; elif [ "$(hash /dev/null)" = none ]; then echo nohash; else echo missing; fi`, posixHash, quotePosix(file), digest)
	answer, err := runSsh(ctx, target, "sh -c "+quotePosix(check), sshRun{})
	if err != nil {
		return "", err
	}
	lines := strings.Split(jsstring.Trim(answer), "\n")
	last := lines[len(lines)-1]
	if last == "" {
		return "missing", nil
	}
	return last, nil
}

// DeployDaemon makes sure the daemon is on the remote machine, verified by its SHA-256 before it ever runs, and removes
// daemons of other contents. The upload goes to a new temporary file and is renamed into place only once its hash
// matches. It returns the remote path of the binary. A nil binary is the packaged daemon for the system; a set path is
// used as given, so an empty one names no file.
func DeployDaemon(ctx context.Context, target SshTarget, remote RemotePlatform, binaryOption *string) (string, error) {
	binary := PackagedDaemon(remote)
	if binaryOption != nil {
		binary = *binaryOption
	}
	// A missing binary names the system; one that exists but cannot be read fails with its read error, as readFile
	// after existsSync does.
	content, err := os.ReadFile(binary)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("No pi-env daemon for %s-%s at %s", remote.Platform, remote.Arch, binary)
	}
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])
	file := daemonPath(remote, digest)
	state, err := checkDaemon(ctx, target, remote, file, digest)
	if err != nil {
		return "", err
	}
	if state == "present" {
		return file, nil
	}
	if state == "nohash" {
		return "", fmt.Errorf("%s has no sha256sum, shasum or openssl to verify pi-env", target.Host)
	}
	if remote.Platform == "windows" {
		upload := powershell(strings.Join([]string{
			"$ErrorActionPreference = 'Stop'",
			"$f = " + quotePowerShell(file),
			"$d = Split-Path -Parent $f",
			"New-Item -ItemType Directory -Force -Path $d | Out-Null",
			"$t = Join-Path $d ('.pi-env-' + [guid]::NewGuid().ToString() + '.tmp')",
			// PowerShell reads redirected stdin itself, as text lines for `$input`, so the binary comes as base64 lines
			// up to an end marker: Windows' sshd may never pass on the end of stdin.
			"$text = New-Object System.Text.StringBuilder",
			"foreach ($line in $input) { if ($line -eq 'PI-ENV-END') { break }; [void]$text.Append($line) }",
			"$bytes = [Convert]::FromBase64String($text.ToString())",
			"$out = [IO.File]::Open($t, 'CreateNew', 'Write', 'None'); $out.Write($bytes, 0, $bytes.Length); $out.Close()",
			fmt.Sprintf("if ((Get-FileHash -Algorithm SHA256 -LiteralPath $t).Hash.ToLower() -ne '%s') { Remove-Item -LiteralPath $t; throw 'pi-env upload is corrupt' }", digest),
			// A running daemon or a virus scanner can hold the old file for a moment.
			"for ($i = 0; ; $i++) { try { Move-Item -Force -LiteralPath $t -Destination $f; break } catch { if ($i -ge 20) { throw }; Start-Sleep -Milliseconds 250 } }",
			// Older daemons; one that is running stays until it exits.
			"Get-ChildItem -LiteralPath $d -Filter 'pi-env-*.exe' | Where-Object { $_.FullName -ne $f } | ForEach-Object { Remove-Item -LiteralPath $_.FullName -ErrorAction SilentlyContinue }",
			"'deployed'",
		}, "; "))
		encoded := base64.StdEncoding.EncodeToString(content)
		var lines strings.Builder
		for len(encoded) > 0 {
			size := min(76, len(encoded))
			lines.WriteString(encoded[:size])
			lines.WriteString("\n")
			encoded = encoded[size:]
		}
		lines.WriteString("PI-ENV-END\n")
		if _, err := runSsh(ctx, target, upload, sshRun{stdin: []byte(lines.String()), timeout: uploadTimeout}); err != nil {
			return "", err
		}
		return file, nil
	}
	upload := strings.Join([]string{
		"set -e",
		posixHash,
		"f=" + quotePosix(file),
		`d=$(dirname "$f")`,
		`mkdir -p "$d"`,
		`chmod 700 "$d"`,
		`t=$(mktemp "$d/.pi-env.XXXXXX")`,
		`cat > "$t"`,
		fmt.Sprintf(`if [ "$(hash "$t")" != %s ]; then rm -f "$t"; echo "pi-env upload is corrupt" >&2; exit 1; fi`, digest),
		`chmod 700 "$t"`,
		`mv -f "$t" "$f"`,
		// Older daemons; running ones keep their file open until they exit.
		`for old in "$d"/pi-env-*; do [ "$old" = "$f" ] || rm -f "$old"; done`,
		"echo deployed",
	}, "\n")
	if _, err := runSsh(ctx, target, "sh -c "+quotePosix(upload), sshRun{stdin: content, timeout: uploadTimeout}); err != nil {
		return "", err
	}
	return file, nil
}

// SshConnectOptions are the options of SshConnection and ConnectSsh: the target, plus the binary to deploy (default:
// the one shipped for the remote system).
type SshConnectOptions struct {
	SshTarget
	// Binary is the daemon to deploy; nil is the packaged one.
	Binary *string
	// LoginShell starts the daemon through the user's login shell (`$SHELL -l`) on POSIX, so commands see the
	// environment of ~/.profile and similar files. Off by default: ssh runs commands without a login shell.
	LoginShell bool
	OnLog      func(text string)
}

// launchCommand is the remote command that starts the daemon at file, before the `serve` arguments.
func launchCommand(remote RemotePlatform, file string, loginShell bool) string {
	if remote.Platform == "windows" {
		// PowerShell runs a quoted path only with the call operator; cmd.exe keeps one pair of quotes around a program.
		if remote.Shell == "powershell" {
			return "& " + quotePowerShell(file)
		}
		if strings.Contains(file, " ") {
			return `"` + file + `"`
		}
		return file
	}
	// $0 is the daemon, "$@" its arguments; $SHELL is the login shell's own name for itself.
	if loginShell {
		return `exec "$SHELL" -lc 'exec "$0" "$@"' ` + quotePosix(file)
	}
	return quotePosix(file)
}

// sshState is what an SSH connection learned about its remote system, and the daemon it verified last.
type sshState struct {
	mu       sync.Mutex
	remote   *RemotePlatform
	verified string
}

// sshConnectionFor is a Connection whose every start detects the remote system (once), verifies or deploys the
// daemon, then starts it.
func sshConnectionFor(options SshConnectOptions, state *sshState) *Connection {
	return NewConnection(ConnectionOptions{
		// The Connection runs one start at a time; the lock guards only the state, which remote() reads meanwhile.
		CommandFunc: func(ctx context.Context) ([]string, error) {
			state.mu.Lock()
			remote := state.remote
			state.mu.Unlock()
			if remote == nil {
				detected, err := DetectPlatform(ctx, options.SshTarget)
				if err != nil {
					return nil, err
				}
				if options.OnLog != nil {
					for _, warning := range detected.Warnings {
						options.OnLog(warning + "\n")
					}
				}
				remote = &detected
				state.mu.Lock()
				state.remote = remote
				state.mu.Unlock()
			}
			// A deployment that just verified the binary counts for the first start.
			state.mu.Lock()
			file := state.verified
			state.verified = ""
			state.mu.Unlock()
			if file == "" {
				var err error
				if file, err = DeployDaemon(ctx, options.SshTarget, *remote, options.Binary); err != nil {
					return nil, err
				}
			}
			arguments, err := SshArguments(options.SshTarget)
			if err != nil {
				return nil, err
			}
			return append(append([]string{sshProgram(options.SshTarget)}, arguments...), launchCommand(*remote, file, options.LoginShell)), nil
		},
		OnLog: options.OnLog,
	})
}

// SshConnection is a Connection to the target that does nothing until its first request. Each start detects the
// remote system (the first time), verifies the daemon and deploys it if it is missing or changed, then starts it over
// ssh. A failure (no network, an untrusted or changed host key) fails the requests waiting for that start with code
// spawn_error and the ssh diagnostics as message; the next request tries again. remote returns the detected system,
// once known.
func SshConnection(options SshConnectOptions) SshConnected {
	return sshConnected(options, &sshState{})
}

// sshConnected builds the lazy connection and the accessor for the detected system over state.
func sshConnected(options SshConnectOptions, state *sshState) SshConnected {
	return SshConnected{Connection: sshConnectionFor(options, state), Remote: func() *RemotePlatform {
		state.mu.Lock()
		defer state.mu.Unlock()
		return state.remote
	}}
}

// SshConnected is what SshConnection returns, upstream's `{ connection, remote() }`.
type SshConnected struct {
	// Connection starts the daemon over ssh on its first request.
	Connection *Connection
	// Remote is the detected system, or nil until a start detected it.
	Remote func() *RemotePlatform
}

// ConnectSsh detects the remote system and deploys the daemon now, then returns a Connection that starts it over ssh,
// like SshConnection. Failures return here: host keys must already be trusted (ScanHostKey, AcceptHostKey), otherwise
// this fails with HostKeyUnknownError.
func ConnectSsh(ctx context.Context, options SshConnectOptions) (*Connection, RemotePlatform, error) {
	remote, err := DetectPlatform(ctx, options.SshTarget)
	if err != nil {
		return nil, RemotePlatform{}, err
	}
	verified, err := DeployDaemon(ctx, options.SshTarget, remote, options.Binary)
	if err != nil {
		return nil, RemotePlatform{}, err
	}
	if options.OnLog != nil {
		for _, warning := range remote.Warnings {
			options.OnLog(warning + "\n")
		}
	}
	return sshConnectionFor(options, &sshState{remote: &remote, verified: verified}), remote, nil
}
