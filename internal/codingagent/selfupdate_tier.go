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

// osCmdRunner runs commands through os/exec, applying the configured npm
// command when probing npm's global root just like upstream.
type osCmdRunner struct {
	npmCommand []string
	ctx        context.Context
}

const packageManagerProbeTimeout = 5 * time.Second

const packageManagerUpdateTimeout = 10 * time.Minute

func (r osCmdRunner) Output(name string, args ...string) (string, error) {
	return r.output(name, args...)
}

func (r osCmdRunner) output(name string, args ...string) (string, error) {
	if name == "npm" && len(r.npmCommand) > 0 && r.npmCommand[0] == "npm" {
		name, args = r.npmCommand[0], append(append([]string{}, r.npmCommand[1:]...), args...)
	}
	ctx := r.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// ResolveSelfUpdateTier proves exactly one installation owner for the running
// executable before any mutation. It never mutates state. Ambiguous ownership
// returns a *TierError and the caller must refuse to update.
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
	return resolveSelfUpdateTierForExe(exe, osCmdRunner{
		npmCommand: settings.GetGlobalSettings().NpmCommand,
		ctx:        ctx,
	})
}

// resolveSelfUpdateTierForExe classifies a given executable path with an
// injectable command runner. It is the testable core; ResolveSelfUpdateTier
// supplies the real os.Executable() and os/exec runner.
func resolveSelfUpdateTierForExe(exe string, runner cmdRunner) (*SelfUpdateProvenance, error) {
	return resolveSelfUpdateTierOn(runtime.GOOS, exe, runner)
}

// resolveSelfUpdateTierOn classifies exe as an installation on goos.
func resolveSelfUpdateTierOn(goos, exe string, runner cmdRunner) (*SelfUpdateProvenance, error) {
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
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PIG_INSTALL_TIER"))) {
	case "container", "image":
		return &SelfUpdateProvenance{Tier: tierContainer, ExePath: exe}, nil
	case "immutable-binary", "piglet-binary":
		return &SelfUpdateProvenance{Tier: tierImmutableBinary, ExePath: exe}, nil
	case "":
		// fall through to filesystem detection
	default:
		return nil, &TierError{
			Tier:        tierUnsupported,
			ExePath:     exe,
			Remediation: fmt.Sprintf("unknown PIG_INSTALL_TIER value %q; unset it or set it to one of: container, image, immutable-binary, piglet-binary", os.Getenv("PIG_INSTALL_TIER")),
		}
	}

	if owner, pkg, ok, ambiguous := detectPackageManagerOwnership(exe, runner); ambiguous {
		return nil, &TierError{
			Tier:        tierPackageManager,
			ExePath:     exe,
			Remediation: fmt.Sprintf("pig executable %s is managed by more than one package manager; update it with the single manager that owns it", exe),
		}
	} else if ok {
		if goos == "windows" && owner != ownerNPM && owner != ownerPNPM {
			return nil, &TierError{
				Tier:        tierUnsupported,
				ExePath:     exe,
				Remediation: fmt.Sprintf("%s self-update on Windows is only supported for npm and pnpm installs.\nDetected install method: %s. Update %s manually.", AppName, owner, AppName),
			}
		}
		if !isWritablePackageManagerPath(exe) {
			return &SelfUpdateProvenance{Tier: tierUnsupported, ExePath: exe}, nil
		}
		return &SelfUpdateProvenance{
			Tier:         tierPackageManager,
			ExePath:      exe,
			PackageOwner: owner,
			PackageName:  pkg,
		}, nil
	}

	if goos == "windows" {
		// pig divergence (D39): a standalone pig.exe is not replaced in place.
		return &SelfUpdateProvenance{Tier: tierUnsupported, ExePath: exe}, nil
	}

	// Writability is not ownership. A standalone installer receipt must bind the
	// canonical executable, running release, update source, and installed digest
	// before the in-place replacement tier is selected.
	if isWritableReplacement(exe) && validateStandaloneReceipt(exe) == nil {
		return &SelfUpdateProvenance{Tier: tierStandalone, ExePath: exe}, nil
	}

	return &SelfUpdateProvenance{Tier: tierUnsupported, ExePath: exe}, nil
}

// detectPackageManagerOwnership reports whether exe lives inside a global
// package-manager root, returning the owning manager and package name. The
// boolean ambiguous is true when two or more managers claim the path.
//
// A package-manager install is only "owned" when the executable resolves under
// one of the manager's global roots. This mirrors upstream
// isManagedByGlobalPackageManager without assuming a Node entrypoint.
func detectPackageManagerOwnership(exe string, runner cmdRunner) (owner PackageManagerOwner, pkg string, ok, ambiguous bool) {
	// Canonicalize both sides through EvalSymlinks so a temp-dir under a
	// symlinked prefix (macOS /var → /private/var) compares equal to a
	// package-manager root reported with the un-resolved prefix. Mirrors
	// upstream getPathComparisonCandidates realpathSync.
	exeNorm := canonicalPath(exe)
	// Track the set of distinct managers that claim the path. Multiple roots
	// from the same manager (e.g. npm root -g plus its parent) are one owner,
	// not ambiguity; only two or more distinct managers is ambiguous.
	var owners map[PackageManagerOwner]struct{}
	for _, m := range []struct {
		owner    PackageManagerOwner
		rootsFor func(cmdRunner) []string
	}{
		{ownerNPM, npmGlobalRoots},
		{ownerPNPM, pnpmGlobalRoots},
		{ownerYarn, yarnGlobalRoots},
		{ownerBun, bunGlobalRoots},
	} {
		for _, root := range m.rootsFor(runner) {
			if root == "" {
				continue
			}
			rootNorm := canonicalPath(root)
			if exeNorm == rootNorm || strings.HasPrefix(exeNorm, rootNorm+string(filepath.Separator)) {
				if owners == nil {
					owners = map[PackageManagerOwner]struct{}{}
				}
				if _, seen := owners[m.owner]; !seen {
					owners[m.owner] = struct{}{}
					if len(owners) == 1 {
						owner = m.owner
						pkg = inferOwningPackage(exe, rootNorm)
					}
				}
				break // one match per manager is enough
			}
		}
	}
	if len(owners) >= 2 {
		return owner, pkg, true, true
	}
	return owner, pkg, len(owners) == 1, false
}

// inferOwningPackage extracts the package name from an executable path inside a
// global node_modules root. For a scoped package the name is "@scope/name".
func inferOwningPackage(exe, root string) string {
	rel, err := filepath.Rel(root, exe)
	if err != nil {
		return PackageName
	}
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	// pnpm's executable resolves through
	// node_modules/.pnpm/<store-entry>/node_modules/<package>/... . The package
	// after the last node_modules segment is the owner; the store directory is
	// not a package identity.
	for i, part := range slices.Backward(parts) {
		if part != "node_modules" || i+1 >= len(parts) {
			continue
		}
		name := parts[i+1]
		if strings.HasPrefix(name, "@") && i+2 < len(parts) {
			return name + "/" + parts[i+2]
		}
		return name
	}
	if filepath.Base(filepath.Clean(root)) == "node_modules" && len(parts) > 0 {
		if strings.HasPrefix(parts[0], "@") && len(parts) > 1 {
			return parts[0] + "/" + parts[1]
		}
		return parts[0]
	}
	return PackageName
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
	// packageManagerSelfUpdateCommand switches on the first element of
	// npmCommand; feed it the owner's command name so a proven pnpm/yarn/bun
	// owner produces the right command regardless of configured npm args.
	ownerCmd := []string{string(owner)}
	if owner == ownerNPM {
		ownerCmd = npmCommand
		if len(ownerCmd) == 0 {
			ownerCmd = []string{"npm"}
		}
	}
	base := packageManagerSelfUpdateCommand(installedPackage, ownerCmd, target)
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
		c := exec.CommandContext(ctx, name, args...)
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
		return SelfUpdateActionResult{Action: "refused", Message: UnsupportedRemediation(prov.ExePath)}
	}
}
