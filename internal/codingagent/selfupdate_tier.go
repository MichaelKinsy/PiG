package codingagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/crossspawn"
)

// pig divergence (D39): SelfUpdateTier selects one owner before mutation.
type SelfUpdateTier string

const (
	// TierStandalone is a writable standalone binary that pig replaces in
	// place from the configured update source.
	TierStandalone SelfUpdateTier = "standalone"
	// TierPackageManager is an installation owned by a global npm/pnpm/yarn/bun
	// install. The owning manager's exact command applies the update; pig does
	// not overwrite its file.
	TierPackageManager SelfUpdateTier = "package-manager"
	// TierImmutableBinary is a baked Piglet/Piglet Binary release. Its owning
	// release is rebuilt and re-pulled; pig does not drift the baked
	// composition in place.
	TierImmutableBinary SelfUpdateTier = "immutable-binary"
	// TierContainer is an OCI image or Piglet Image deployment. The
	// authenticated pull/redeploy path is reported; pig does not rewrite a
	// running image.
	TierContainer SelfUpdateTier = "container"
	// TierUnsupported covers read-only, Windows in-place-unsupported, and
	// unknown-provenance installations. Pig refuses mutation and reports the
	// executable path plus concrete remediation.
	TierUnsupported SelfUpdateTier = "unsupported"
)

// internal aliases keep the resolver body readable after the export.
const (
	tierStandalone      = TierStandalone
	tierPackageManager  = TierPackageManager
	tierImmutableBinary = TierImmutableBinary
	tierContainer       = TierContainer
	tierUnsupported     = TierUnsupported
)

// PackageManagerOwner names a package manager proven to own an installation.
type PackageManagerOwner string

const (
	ownerNPM  PackageManagerOwner = "npm"
	ownerPNPM PackageManagerOwner = "pnpm"
	ownerYarn PackageManagerOwner = "yarn"
	ownerBun  PackageManagerOwner = "bun"
)

// SelfUpdateProvenance is the resolved ownership of the running executable.
// Exactly one Tier is set; ambiguity is reported as an error rather than a
// mixed tier.
type SelfUpdateProvenance struct {
	Tier         SelfUpdateTier
	ExePath      string
	PackageOwner PackageManagerOwner
	// PackageName is the owning package spec when Tier == tierPackageManager.
	PackageName string
	// PackageDir is the owning package directory, not copied metadata below dist or a launcher directory.
	PackageDir string
	// NpmPrefix is retained only from a proven lib/node_modules root.
	NpmPrefix string
}

// TierError reports that tier resolution could not select exactly one owner,
// or that the selected tier refused mutation. It carries the executable path
// and a concrete, non-looping remediation so the caller surfaces it verbatim.
type TierError struct {
	Tier        SelfUpdateTier
	ExePath     string
	Remediation string
	Cause       error
}

func (e *TierError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Remediation, e.Cause)
	}
	return e.Remediation
}

func (e *TierError) Unwrap() error { return e.Cause }

// cmdRunner abstracts running a package-manager probe command so tests can
// supply deterministic output without spawning real processes.
type cmdRunner interface {
	Output(name string, args ...string) (string, error)
}

// osCmdRunner preserves configured npm executables and leading arguments. Probe output uses JavaScript whitespace trimming.
type osCmdRunner struct {
	npmCommand       []string
	ctx              context.Context
	preparedNpmRoot  *string
	preparedNpmError error
}

const packageManagerProbeTimeout = 5 * time.Second

const packageManagerUpdateTimeout = 10 * time.Minute

func (r osCmdRunner) Output(name string, args ...string) (string, error) {
	return r.output(name, args...)
}

func (r osCmdRunner) output(name string, args ...string) (string, error) {
	if r.preparedNpmRoot != nil && name == "npm" && len(args) == 2 && args[0] == "root" && args[1] == "-g" {
		return *r.preparedNpmRoot, r.preparedNpmError
	}
	if name == "npm" && len(r.npmCommand) > 0 {
		name, args = r.npmCommand[0], append(append([]string{}, r.npmCommand[1:]...), args...)
	}
	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	out, err := crossspawn.Command(ctx, "", name, args...).Output()
	return jsTrim(string(out)), err
}

// preparePackageManagerProbes records an explicit npm root query once per resolution. Its error cannot veto another proven installation owner.
// upstream: packages/coding-agent/src/config.ts:readCommandOutput
func preparePackageManagerProbes(runner cmdRunner) (cmdRunner, error) {
	var r osCmdRunner
	switch value := runner.(type) {
	case osCmdRunner:
		r = value
	case *osCmdRunner:
		r = *value
	default:
		return runner, nil
	}
	if len(r.npmCommand) == 0 {
		return runner, nil
	}
	root, err := r.Output("npm", "root", "-g")
	if err != nil {
		if r.ctx != nil && r.ctx.Err() != nil {
			err = r.ctx.Err()
		}
		command := strings.Join(append(slices.Clone(r.npmCommand), "root", "-g"), " ")
		err = &configuredNpmProbeError{command: command, cause: err}
	}
	r.preparedNpmRoot = &root
	r.preparedNpmError = err
	return r, err
}

type configuredNpmProbeError struct {
	command string
	cause   error
}

func (e *configuredNpmProbeError) Error() string {
	reason := e.cause.Error()
	if exit, ok := errors.AsType[*exec.ExitError](e.cause); ok {
		reason = jsTrim(string(exit.Stderr))
		if reason == "" {
			code := "unknown"
			if exit.ExitCode() >= 0 {
				code = fmt.Sprint(exit.ExitCode())
			}
			reason = "exit code " + code
		}
	}
	return "Failed to run " + e.command + ": " + reason
}

func (e *configuredNpmProbeError) Unwrap() error { return e.cause }

// ResolveSelfUpdateTier proves exactly one installation owner for the running
// executable before any mutation. It never mutates state. Ambiguous ownership
// returns a *TierError and the caller must refuse to update. Explicit npm probe failures surface unless another proven owner selects the route.
//
// Resolution order follows the tier ladder: explicit product override
// (Piglet Binary baked version, or PIG_INSTALL_TIER for deployments), then
// package-manager ownership, then writable standalone, then read-only/Windows/
// unknown remediation.
func ResolveSelfUpdateTier() (*SelfUpdateProvenance, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate pig executable: %w", err)
	}
	cwd, _ := os.Getwd()
	settings := NewSettingsManager(cwd, AgentDir())
	ctx, cancel := context.WithTimeout(context.Background(), packageManagerProbeTimeout)
	defer cancel()
	entrypoint := os.Args[0]
	if resolved, err := exec.LookPath(entrypoint); err == nil {
		entrypoint = resolved
	}
	return resolveSelfUpdateTierOn(runtime.GOOS, exe, osCmdRunner{
		npmCommand: settings.GetGlobalSettings().NpmCommand,
		ctx:        ctx,
	}, entrypoint)
}

// resolveSelfUpdateTierForExe classifies a given executable path with an
// injectable command runner. It is the testable core; ResolveSelfUpdateTier
// supplies the real os.Executable() and os/exec runner.
func resolveSelfUpdateTierForExe(exe string, runner cmdRunner) (*SelfUpdateProvenance, error) {
	return resolveSelfUpdateTierOn(runtime.GOOS, exe, runner)
}

// resolveSelfUpdateTierOn classifies exe as an installation on goos.
func resolveSelfUpdateTierOn(goos, exe string, runner cmdRunner, entrypoints ...string) (*SelfUpdateProvenance, error) {
	entrypoints = append([]string{exe}, entrypoints...)
	// Follow symlinks to the real target so ownership and writability are
	// evaluated against the binary pig would actually replace. Mirrors
	// upstream realpathSync in isManagedByGlobalPackageManager.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	// A baked Piglet/Piglet Binary release is immutable by construction: its
	// composition is fixed at build time and cannot be drifted in place.
	if PigletBinaryRelease != "" {
		return &SelfUpdateProvenance{
			Tier:    tierImmutableBinary,
			ExePath: exe,
		}, nil
	}

	// PIG_INSTALL_TIER lets a product deployment declare its provenance when
	// filesystem detection is unreliable inside a container or image. Only
	// container provenance is accepted this way: a deployment cannot claim
	// standalone or package-manager ownership without filesystem proof, and
	// must not anonymously re-enter a mutating tier.
	var npmProbeErr error
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PIG_INSTALL_TIER"))) {
	case "container", "image":
		return &SelfUpdateProvenance{Tier: tierContainer, ExePath: exe}, nil
	case "immutable-binary", "piglet-binary":
		return &SelfUpdateProvenance{Tier: tierImmutableBinary, ExePath: exe}, nil
	case "":
		runner, npmProbeErr = preparePackageManagerProbes(runner)
	default:
		return nil, &TierError{
			Tier:        tierUnsupported,
			ExePath:     exe,
			Remediation: fmt.Sprintf("unknown PIG_INSTALL_TIER value %q; unset it or set it to one of: container, image, immutable-binary, piglet-binary", os.Getenv("PIG_INSTALL_TIER")),
		}
	}

	verified := make([]string, 0, len(entrypoints))
	for _, entrypoint := range entrypoints {
		resolved, err := filepath.EvalSymlinks(entrypoint)
		if err == nil && ownershipPath(goos, resolved) == ownershipPath(goos, exe) {
			verified = append(verified, entrypoint)
		}
	}
	if owned, ambiguous := detectPackageManagerOwnership(goos, exe, runner, verified...); ambiguous {
		return nil, &TierError{
			Tier:        tierPackageManager,
			ExePath:     exe,
			Remediation: fmt.Sprintf("pig executable %s is managed by more than one package manager; update it with the single manager that owns it", exe),
		}
	} else if owned != nil {
		if goos == "windows" && owned.PackageOwner != ownerNPM && owned.PackageOwner != ownerPNPM {
			return nil, &TierError{
				Tier:        tierUnsupported,
				ExePath:     exe,
				Remediation: fmt.Sprintf("%s self-update on Windows is only supported for npm and pnpm installs.\nDetected install method: %s. Update %s manually.", AppName, owned.PackageOwner, AppName),
			}
		}
		if !isWritablePackageManagerPath(exe) || !directoryWritable(owned.PackageDir) || !directoryWritable(filepath.Dir(owned.PackageDir)) {
			owned.Tier = tierUnsupported
		}
		return owned, nil
	}

	if goos == "windows" {
		if npmProbeErr != nil {
			return nil, npmProbeErr
		}
		// pig divergence (D39): a standalone pig.exe is not replaced in place.
		return &SelfUpdateProvenance{Tier: tierUnsupported, ExePath: exe}, nil
	}

	// Writability is not ownership. A standalone installer receipt must bind the
	// canonical executable, running release, update source, and installed digest
	// before the in-place replacement tier is selected.
	if isWritableReplacement(exe) && validateStandaloneReceipt(exe) == nil {
		return &SelfUpdateProvenance{Tier: tierStandalone, ExePath: exe}, nil
	}

	if npmProbeErr != nil {
		return nil, npmProbeErr
	}

	return &SelfUpdateProvenance{Tier: tierUnsupported, ExePath: exe}, nil
}

// detectPackageManagerOwnership accepts only manager roots. The caller verifies additional entrypoints resolve to exe. Windows comparisons ignore case and separator spelling.
func detectPackageManagerOwnership(goos, exe string, runner cmdRunner, entrypoints ...string) (*SelfUpdateProvenance, bool) {
	candidates := append([]string{exe}, entrypoints...)
	owners := map[PackageManagerOwner]bool{}
	var result *SelfUpdateProvenance
	for _, manager := range []struct {
		owner    PackageManagerOwner
		rootsFor func(cmdRunner) []string
	}{{ownerNPM, npmGlobalRoots}, {ownerPNPM, pnpmGlobalRoots}, {ownerYarn, yarnGlobalRoots}, {ownerBun, bunGlobalRoots}} {
		for _, root := range manager.rootsFor(runner) {
			if root == "" {
				continue
			}
			matched := false
			for _, candidate := range candidates {
				for _, candidatePath := range []string{candidate, canonicalPath(candidate)} {
					for _, rootPath := range []string{root, canonicalPath(root)} {
						exeNorm, rootNorm := ownershipPathLiteral(goos, candidatePath), ownershipPathLiteral(goos, rootPath)
						if exeNorm == rootNorm || strings.HasPrefix(exeNorm, strings.TrimSuffix(rootNorm, "/")+"/") {
							matched = true
						}
					}
				}
			}
			if !matched {
				continue
			}
			owners[manager.owner] = true
			if result == nil {
				pkg, directory := owningPackageFromExecutable(goos, exe)
				prefix := ""
				if manager.owner == ownerNPM && filepath.Base(filepath.Clean(root)) == "node_modules" && filepath.Base(filepath.Dir(root)) == "lib" {
					prefix = filepath.Dir(filepath.Dir(root))
				}
				result = &SelfUpdateProvenance{Tier: tierPackageManager, ExePath: exe, PackageOwner: manager.owner, PackageName: pkg, PackageDir: directory, NpmPrefix: prefix}
			}
			break
		}
	}
	return result, len(owners) > 1
}

func ownershipPath(goos, value string) string {
	return ownershipPathLiteral(goos, canonicalPath(value))
}

func ownershipPathLiteral(goos, value string) string {
	value = filepath.ToSlash(value)
	if goos == "windows" {
		value = strings.ToLower(strings.ReplaceAll(value, "\\", "/"))
	}
	return strings.TrimSuffix(value, "/")
}

func owningPackageFromExecutable(goos, exe string) (string, string) {
	path := filepath.ToSlash(exe)
	if goos == "windows" {
		path = strings.ReplaceAll(path, "\\", "/")
	}
	parts := strings.Split(path, "/")
	for i, part := range slices.Backward(parts) {
		isModules := part == "node_modules" || goos == "windows" && strings.EqualFold(part, "node_modules")
		if !isModules || i+1 >= len(parts) {
			continue
		}
		end := i + 2
		if strings.HasPrefix(parts[i+1], "@") && i+2 < len(parts) {
			end++
		}
		name := strings.Join(parts[i+1:end], "/")
		if isNpmPlatformPackage(name) {
			return PackageName, filepath.FromSlash(launcherPackageDir(goos, parts[:i]))
		}
		return name, filepath.FromSlash(strings.Join(parts[:end], "/"))
	}
	return PackageName, filepath.Dir(exe)
}

// The npm os and cpu names of the platform packages that carry the native
// binary: automation/release/npm/pack_npm.py TARGETS.
var (
	npmPlatformOSes = []string{"darwin", "linux", "win32"}
	npmPlatformCPUs = []string{"arm64", "x64"}
)

// isNpmPlatformPackage reports whether name is one of the launcher's platform
// packages, "<launcher>-<os>-<cpu>". The launcher package PackageName owns the
// installation; a platform package only holds the executable.
func isNpmPlatformPackage(name string) bool {
	suffix, ok := strings.CutPrefix(name, PackageName+"-")
	if !ok {
		return false
	}
	npmOS, npmCPU, ok := strings.Cut(suffix, "-")
	return ok && slices.Contains(npmPlatformOSes, npmOS) && slices.Contains(npmPlatformCPUs, npmCPU)
}

// launcherPackageDir returns the directory of the launcher package for a
// platform package installed under the node_modules directory whose path
// segments, without the trailing node_modules entry, are parts. npm nests the
// platform package inside the launcher; yarn and bun hoist it beside the
// launcher; pnpm keeps it in its own virtual-store entry, so the launcher lives
// in the node_modules directory that holds the .pnpm store.
func launcherPackageDir(goos string, parts []string) string {
	launcher := strings.Split(PackageName, "/")
	if n := len(launcher); len(parts) >= n && samePathSegments(goos, parts[len(parts)-n:], launcher) {
		return strings.Join(parts, "/")
	}
	for i, part := range parts {
		if part == ".pnpm" {
			return strings.Join(append(slices.Clone(parts[:i]), PackageName), "/")
		}
	}
	return strings.Join(append(slices.Clone(parts), "node_modules", PackageName), "/")
}

func samePathSegments(goos string, got, want []string) bool {
	return slices.EqualFunc(got, want, func(a, b string) bool {
		return a == b || goos == "windows" && strings.EqualFold(a, b)
	})
}

func npmGlobalRoots(runner cmdRunner) []string {
	root := probeOutput(runner, "npm", "root", "-g")
	if root == "" {
		return nil
	}
	return []string{root}
}

func pnpmGlobalRoots(runner cmdRunner) []string {
	root := probeOutput(runner, "pnpm", "root", "-g")
	if root == "" {
		return nil
	}
	return []string{root, filepath.Dir(root)}
}

func yarnGlobalRoots(runner cmdRunner) []string {
	dir := probeOutput(runner, "yarn", "global", "dir")
	if dir == "" {
		return nil
	}
	return []string{dir, filepath.Join(dir, "node_modules")}
}

func bunGlobalRoots(runner cmdRunner) []string {
	home, _ := os.UserHomeDir()
	roots := []string{filepath.Join(home, ".bun", "install", "global", "node_modules")}
	if bin := probeOutput(runner, "bun", "pm", "bin", "-g"); bin != "" {
		roots = append(roots, filepath.Join(filepath.Dir(bin), "install", "global", "node_modules"))
	}
	return roots
}

func probeOutput(runner cmdRunner, name string, args ...string) string {
	out, err := runner.Output(name, args...)
	if err != nil || out == "" {
		return ""
	}
	return out
}

func isWritablePackageManagerPath(path string) bool {
	return isWritableReplacement(path)
}

func isWritableReplacement(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return replacementDirectoryWritable(path)
}

// PackageManagerUpdateCommand returns the exact command the owning manager runs
// to update its installation. Mirrors upstream getSelfUpdateCommandForMethod
// command construction including --ignore-scripts. The owner: proven by tier
// resolution: drives the command name; npmCommand supplies optional extra args
// (e.g. a configured registry) for the npm owner only. Returns nil for an
// unknown owner.
func PackageManagerUpdateCommand(owner PackageManagerOwner, installedPackage string, npmCommand []string, target SelfUpdatePackageTarget) *SelfUpdateCommand {
	// The proven owner selects argument semantics independently of a configured executable or wrapper.
	ownerCmd := []string{string(owner)}
	if owner == ownerNPM {
		ownerCmd = npmCommand
		if len(ownerCmd) == 0 {
			ownerCmd = []string{"npm"}
		}
	}
	base := packageManagerSelfUpdateCommand(owner, installedPackage, ownerCmd, target)
	switch owner {
	case ownerNPM, ownerPNPM, ownerYarn, ownerBun:
		return base
	default:
		return nil
	}
}

// packageManagerRunner executes the package-manager update steps. It defaults
// to os/exec so production spawns the real manager; tests override it to prove
// the exact owner command runs and that no standalone fallback begins after
// the tier starts.
var packageManagerRunner = func(cmd *SelfUpdateCommand) error {
	ctx, cancel := context.WithTimeout(context.Background(), packageManagerUpdateTimeout)
	defer cancel()
	return runPackageManagerSteps(cmd, func(name string, args ...string) spawner {
		c := crossspawn.Command(ctx, "", name, args...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		c.Stdin = os.Stdin
		return c
	})
}

// runPackageManagerSteps runs each step of cmd via the supplied spawner. Once
// the first step starts, a failure surfaces from this tier and never falls
// through to another.
func runPackageManagerSteps(cmd *SelfUpdateCommand, spawn func(string, ...string) spawner) error {
	if cmd == nil {
		return errors.New("no package-manager update command")
	}
	steps := cmd.Steps
	if len(steps) == 0 {
		steps = []*SelfUpdateCommand{cmd}
	}
	for _, step := range steps {
		c := spawn(step.Command, step.Args...)
		if err := c.Run(); err != nil {
			return fmt.Errorf("%s: %w", step.Display, err)
		}
	}
	return nil
}

// spawner is the minimal exec.Cmd surface runPackageManagerSteps uses.
type spawner interface {
	Run() error
}

// RunPackageManagerUpdate executes the owning manager's update command. It is
// the only mutation path for the package-manager tier; once it starts, a
// failure surfaces from this tier and never falls through to another.
func RunPackageManagerUpdate(cmd *SelfUpdateCommand) error {
	return packageManagerRunner(cmd)
}

// ImmutableBinaryRemediation is the exact, non-looping instruction for a baked
// Piglet/Piglet Binary release. Pig does not drift the baked composition in
// place; the owning release is rebuilt and re-pulled.
func ImmutableBinaryRemediation(exePath string) string {
	return fmt.Sprintf(
		"%s is an immutable Piglet/Piglet Binary (built release %s). It cannot be updated in place. "+
			"Rebuild it from its Piglet source (`pig piglet build <name> --format binary`) or pull the new release artifact from your provider, then replace %s.",
		AppName, releaseLabel(), exePath)
}

// ContainerRemediation is the exact authenticated pull/redeploy instruction for
// an OCI image or Piglet Image deployment. Raw Pig performs no
// transport and knows no product topology, so a deployment that owns a specific
// redeploy operation supplies it verbatim through PIG_REDEPLOY_INSTRUCTION.
// Pig still owns the surrounding facts (artifact identity, executable, and the
// guarantee that the running image is never rewritten); only the operation comes
// from the product. Without that metadata Pig falls back to the generic image
// pull, which is correct for a plain OCI/container install but not for an
// orchestrator-managed deployment.
func ContainerRemediation(exePath string) string {
	ref := strings.TrimSpace(os.Getenv("PIG_IMAGE_REF"))
	if instruction := strings.TrimSpace(os.Getenv("PIG_REDEPLOY_INSTRUCTION")); instruction != "" {
		if ref != "" {
			return fmt.Sprintf(
				"%s is running from image %s. %s The running image is not rewritten. Executable: %s.",
				AppName, ref, instruction, exePath)
		}
		return fmt.Sprintf(
			"%s is running inside a container or deployment image. %s The running image is not rewritten. Executable: %s.",
			AppName, instruction, exePath)
	}
	if ref != "" {
		return fmt.Sprintf(
			"%s is running from image %s. Pull the new image (authenticate to your registry first: `%s pull %s`) and re-deploy. The running image is not rewritten. Executable: %s.",
			AppName, ref, imagePullCommand(), ref, exePath)
	}
	return fmt.Sprintf(
		"%s is running inside a container or deployment image. Pull the new image (authenticate to your registry first) and re-deploy. The running image is not rewritten. Executable: %s. "+
			"Set PIG_IMAGE_REF to emit the exact image reference and pull command.",
		AppName, exePath)
}

// UnsupportedRemediation is the concrete reinstall/download instruction for a
// read-only, Windows, or unknown-provenance installation. It never repeats the
// failed self-update command.
func UnsupportedRemediation(exePath string) string {
	return fmt.Sprintf(
		"%s cannot self-update this installation. Executable: %s. "+
			"Reinstall it with the package manager, wrapper, or source checkout that provided it, or download a new %s binary from your provider's releases.",
		AppName, exePath, AppName)
}

func releaseLabel() string {
	if v := strings.TrimSpace(PigletBinaryRelease); v != "" {
		return v
	}
	return "unknown"
}

func imagePullCommand() string {
	if c := strings.TrimSpace(os.Getenv("PIG_IMAGE_PULL_CMD")); c != "" {
		return c
	}
	return "docker"
}

// SelfUpdateActionResult is the outcome of attempting one tier's update. The
// caller surfaces Action and Message verbatim; Done marks a successful
// mutation. Cause carries the originating error so the caller can distinguish a
// benign "up to date" from a real failure.
type SelfUpdateActionResult struct {
	Done    bool
	Action  string
	Message string
	Cause   error
}

// ApplySelfUpdateTier resolves exactly one installation tier and applies its
// update, surfacing any failure from the started tier without falling through.
// The standalone download/replace path is supplied via applyStandalone so the
// cmd layer keeps ownership of the HTTP client, manifest, and version compare.
func ApplySelfUpdateTier(
	applyStandalone func(exePath string) error,
	applyPackageManager func(prov *SelfUpdateProvenance) error,
) SelfUpdateActionResult {
	prov, err := ResolveSelfUpdateTier()
	if err != nil {
		if te, ok := errors.AsType[*TierError](err); ok {
			return SelfUpdateActionResult{Action: "refused", Message: te.Error(), Cause: te}
		}
		return SelfUpdateActionResult{Action: "error", Message: err.Error(), Cause: err}
	}
	return applyProvenance(prov, applyStandalone, applyPackageManager)
}

// applyProvenance applies one resolved tier. Once a mutating tier starts, its
// failure surfaces from that tier and never falls through to another. This is
// the no-fallthrough contract (R9); extracted so tests can drive a controlled
// provenance without re-running os.Executable.
func applyProvenance(
	prov *SelfUpdateProvenance,
	applyStandalone func(exePath string) error,
	applyPackageManager func(prov *SelfUpdateProvenance) error,
) SelfUpdateActionResult {
	switch prov.Tier {
	case tierStandalone:
		// Once the standalone download starts, its failure surfaces here and
		// never falls through to another tier.
		if err := applyStandalone(prov.ExePath); err != nil {
			return SelfUpdateActionResult{Action: "standalone-failed", Message: err.Error(), Cause: err}
		}
		return SelfUpdateActionResult{Done: true, Action: "standalone-updated"}
	case tierPackageManager:
		if applyPackageManager == nil {
			return SelfUpdateActionResult{Action: "refused", Message: "package-manager release plan is unavailable; refusing an unpinned update"}
		}
		if err := applyPackageManager(prov); err != nil {
			return SelfUpdateActionResult{Action: "package-manager-failed", Message: fmt.Sprintf("%s\n%s", err.Error(), UnsupportedRemediation(prov.ExePath)), Cause: err}
		}
		return SelfUpdateActionResult{Done: true, Action: "package-manager-updated"}
	case tierImmutableBinary:
		return SelfUpdateActionResult{Action: "refused", Message: ImmutableBinaryRemediation(prov.ExePath)}
	case tierContainer:
		return SelfUpdateActionResult{Action: "refused", Message: ContainerRemediation(prov.ExePath)}
	default:
		return SelfUpdateActionResult{Action: "refused", Message: prov.GetSelfUpdateUnavailableInstruction()}
	}
}
