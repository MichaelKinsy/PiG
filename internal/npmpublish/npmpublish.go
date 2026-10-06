// Package npmpublish runs `npm publish` for Packages and Piglet source on the author's behalf.
//
// The author's own npm does the authentication, including a one-time password, a passkey, and npm trusted publishing from CI. PiG never reads, stores, or forwards an npm token: it passes its environment to npm unchanged and adds `--otp` only when the author gives a code.
//
// pig additive (D18): Packages and Piglet source publish to npm with a dry run by default.
package npmpublish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/linkerexec"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// TrustedPublishingEnv is set by GitHub Actions when the workflow may request an OIDC token. npm then publishes with provenance and no stored credential.
const TrustedPublishingEnv = "ACTIONS_ID_TOKEN_REQUEST_URL"

var (
	distTag  = lazyregexp.New(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	notInReg = lazyregexp.New(`E404|404 Not Found|is not in this registry`)
	otpCode  = lazyregexp.New(`^[0-9A-Za-z]{4,16}$`)
)

var errNPMNotFound = errors.New("npm is not on PATH; install Node.js (npm publishes the package) or set the npmCommand setting")

// Flags are the options `pig piglet publish --to npm` and `pig package publish --to npm` share.
type Flags struct {
	Yes    bool
	DryRun bool
	Tag    string
	Access string
	OTP    string
}

// Parse consumes the shared option at args[*i], advancing *i past its value. It reports false for an option it does not own.
func (f *Flags) Parse(args []string, i *int) (bool, error) {
	arg := args[*i]
	name, inline, hasInline := strings.Cut(arg, "=")
	value := func() (string, error) {
		if hasInline {
			if inline == "" {
				return "", fmt.Errorf("%s requires a value", name)
			}
			return inline, nil
		}
		if *i+1 >= len(args) || strings.HasPrefix(args[*i+1], "-") {
			return "", fmt.Errorf("%s requires a value", name)
		}
		*i++
		return args[*i], nil
	}
	var err error
	switch name {
	case "--yes":
		f.Yes = true
	case "--dry-run":
		f.DryRun = true
	case "--tag":
		f.Tag, err = value()
	case "--access":
		f.Access, err = value()
	case "--otp":
		f.OTP, err = value()
	default:
		return false, nil
	}
	return true, err
}

// Validate rejects option combinations and values npm would reject after the author waited for a build.
func (f Flags) Validate() error {
	switch {
	case f.Yes && f.DryRun:
		return errors.New("--yes and --dry-run cannot be combined")
	case f.Access != "" && f.Access != "public" && f.Access != "restricted":
		return fmt.Errorf("--access must be public or restricted, not %q", f.Access)
	case f.Tag != "" && (!distTag.MatchString(f.Tag) || semver.IsValid("v"+f.Tag)):
		return fmt.Errorf("--tag %q is not a dist-tag npm accepts; use a name such as latest or next, not a version", f.Tag)
	case f.OTP != "" && !otpCode.MatchString(f.OTP):
		return errors.New("--otp must be the one-time code from your authenticator, not a token or password")
	}
	return nil
}

// Package is what to publish. When InPlace is set, npm runs in Dir and publishes the package there, exactly as `npm publish` does for the author. Otherwise Dir is a generated directory that npm receives as its folder argument.
type Package struct {
	Name          string
	Version       string
	Dir           string
	InPlace       bool
	IgnoreScripts bool
}

// Request is one publication.
type Request struct {
	Package
	Flags
	// Label names the kind of package in output: Piglet or Package.
	Label string
	// Notes are printed under the package line of the plan.
	Notes []string
	// Next is printed after a successful publication.
	Next []string
	// Command is the resolved npm argv. Command() supplies the default.
	Command []string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// Command returns the argv that runs npm: the global npmCommand setting, else `npm`. Project settings are not read, so a repository cannot choose the program that receives the author's credentials.
func Command() ([]string, error) {
	sm := codingagent.NewSettingsManagerWithProjectTrust("", codingagent.AgentDir(), false)
	command := packagemanager.DefaultNpmCommand(sm)
	manager, err := packagemanager.PackageManagerName(command)
	if err != nil {
		return nil, err
	}
	switch manager {
	case "pnpm", "bun", "yarn":
		return nil, fmt.Errorf("publishing runs npm, but the npmCommand setting selects %s; unset it or point it at npm", manager)
	}
	return command, nil
}

// TrustedPublishing reports whether the environment can publish with provenance and no stored credential.
func TrustedPublishing() bool { return os.Getenv(TrustedPublishingEnv) != "" }

// Exists reports whether name@version is already on registry, or on the registry npm is configured for when registry is empty. It fails on anything but a clear answer, so a network error never reads as "not published".
func Exists(ctx context.Context, command []string, name, version, workDir, registry string) (bool, error) {
	args := append(append([]string{}, command[1:]...), "view", name+"@"+version, "version")
	if registry != "" {
		args = append(args, "--registry", registry)
	}
	cmd := linkerexec.CommandContext(ctx, command[0], args...)
	cmd.Dir = workDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return strings.TrimSpace(stdout.String()) == version, nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return false, errNPMNotFound
	}
	if notInReg.MatchString(stdout.String() + stderr.String()) {
		return false, nil
	}
	return false, fmt.Errorf("npm view %s@%s failed: %s", name, version, firstLine(stderr.String(), stdout.String(), err.Error()))
}

// Satisfiable reports whether any published version of name satisfies the version range spec, which may be an exact version. It fails on anything but a clear answer.
func Satisfiable(ctx context.Context, command []string, name, spec, workDir string) (bool, error) {
	args := append(append([]string{}, command[1:]...), "view", name+"@"+spec, "version")
	cmd := linkerexec.CommandContext(ctx, command[0], args...)
	cmd.Dir = workDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return strings.TrimSpace(stdout.String()) != "", nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return false, errNPMNotFound
	}
	if notInReg.MatchString(stdout.String() + stderr.String()) {
		return false, nil
	}
	return false, fmt.Errorf("npm view %s@%s failed: %s", name, spec, firstLine(stderr.String(), stdout.String(), err.Error()))
}

// publishRegistry returns publishConfig.registry from the package.json in dir. `npm publish` sends the package there, but `npm view` ignores publishConfig, so the existence check must name it. An empty result selects npm's configured registry.
func publishRegistry(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var manifest struct {
		PublishConfig struct {
			Registry string `json:"registry"`
		} `json:"publishConfig"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", fmt.Errorf("package.json in %s is not valid JSON: %w", dir, err)
	}
	return manifest.PublishConfig.Registry, nil
}

func firstLine(values ...string) string {
	for _, value := range values {
		if line, _, _ := strings.Cut(strings.TrimSpace(value), "\n"); line != "" {
			return line
		}
	}
	return ""
}

// Args returns the npm arguments after the program. dryRun adds `--dry-run`. The one-time code is included only when mask is false.
func (r Request) Args(dryRun, mask bool) []string {
	args := []string{"publish"}
	if !r.InPlace {
		args = append(args, r.Dir)
	}
	if r.Tag != "" {
		args = append(args, "--tag", r.Tag)
	}
	if r.Access != "" {
		args = append(args, "--access", r.Access)
	}
	if r.IgnoreScripts {
		args = append(args, "--ignore-scripts")
	}
	if TrustedPublishing() {
		args = append(args, "--provenance")
	}
	if r.OTP != "" && !dryRun {
		code := r.OTP
		if mask {
			code = "<code>"
		}
		args = append(args, "--otp", code)
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	return args
}

func (r Request) workDir() string {
	if r.InPlace {
		return r.Dir
	}
	return ""
}

// Run checks the registry, then either shows what npm would publish (the default) or publishes with --yes.
func (r Request) Run(ctx context.Context) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if semver.Prerelease("v"+r.Version) != "" && r.Tag == "" {
		return fmt.Errorf("%s %s is a prerelease, and npm refuses to publish a prerelease as `latest`; add --tag next (or another dist-tag)", r.Name, r.Version)
	}
	command := r.Command
	if len(command) == 0 {
		var err error
		if command, err = Command(); err != nil {
			return err
		}
	}
	registry, err := publishRegistry(r.Dir)
	if err != nil {
		return err
	}
	exists, err := Exists(ctx, command, r.Name, r.Version, r.workDir(), registry)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%s@%s is already on npm; published versions are immutable, so raise the version and publish again", r.Name, r.Version)
	}
	dryRun := !r.Yes
	title := r.Label + " npm publish"
	if dryRun {
		title += " dry run"
	}
	_, _ = fmt.Fprintf(r.Stdout, "%s\n%s: %s@%s (not on npm yet)\n", title, r.Label, r.Name, r.Version)
	for _, note := range r.Notes {
		_, _ = fmt.Fprintln(r.Stdout, note)
	}
	if TrustedPublishing() {
		_, _ = fmt.Fprintf(r.Stdout, "Provenance: on (%s is set; npm uses trusted publishing)\n", TrustedPublishingEnv)
	}
	shown := strings.Join(append(append([]string{}, command...), r.Args(false, true)...), " ")
	if dryRun {
		if err := r.runNPM(ctx, command, r.Args(true, false)); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(r.Stdout, "Would run: %s\nDry run only; rerun with --yes to publish.\n", shown)
		return nil
	}
	_, _ = fmt.Fprintf(r.Stdout, "Running: %s\n", shown)
	if err := r.runNPM(ctx, command, r.Args(false, false)); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(r.Stdout, "Published %s@%s to npm.\n", r.Name, r.Version)
	for _, line := range r.Next {
		_, _ = fmt.Fprintln(r.Stdout, line)
	}
	return nil
}

func (r Request) runNPM(ctx context.Context, command, args []string) error {
	cmd := linkerexec.CommandContext(ctx, command[0], append(append([]string{}, command[1:]...), args...)...)
	cmd.Dir = r.workDir()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = r.Stdin, r.Stdout, r.Stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errNPMNotFound
		}
		return fmt.Errorf("npm publish of %s@%s failed: %w; npm's output is above", r.Name, r.Version, err)
	}
	return nil
}
