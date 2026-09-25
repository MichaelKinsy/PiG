package pigletbuild

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"golang.org/x/mod/semver"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	pigletrelease "github.com/MichaelKinsy/PiG/coding/piglet/release"
	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

const publishUsageLine = "Usage: pig piglet publish <name|path> --to github --repo <owner/repo> --sign-key <key> [--targets os/arch,...] [--artifacts <dir>] [--dry-run|--yes]"

const publishUsage = publishUsageLine + `

Publish one GitHub Release named v<release.version> with a signed Piglet Binary
for each target, SHA256SUMS, and a signed piglet-release.json index. Without
--yes, publish is a dry run: it validates the release and prints the planned
assets without building or uploading anything. PiG runs the GitHub CLI (gh)
for GitHub access and never handles a GitHub token.

Options:
  --to github                   Publish to GitHub Releases
  --repo <owner/repo>           Repository that receives the release
  --sign-key <path>             Ed25519 key that signs every Binary and the index
  --targets <os/arch,...>       Targets to publish (default: build.targets, else the
                                host target, or every Binary in --artifacts)
  --artifacts <dir>             Publish prebuilt signed Binaries from dir; nothing is built
  --builder <auto|native|name>  Builder for each target when building (default auto)
  --commit <sha>                Commit for a new release tag (default: the default branch)
  --source-ref <ref>            npm or Git source recorded in the index (default:
                                git:github.com/<owner/repo>@<commit or tag>)
  --workspace <path>            Resolve workspace-bound Piglet inputs from this path
  --dry-run                     Validate and print the plan only (default)
  --yes                         Build, sign, and upload the release
  --no-input                    Do not prompt
  -h, --help                    Show this help
`

var (
	publishTargetPartPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	publishCommitPattern     = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
)

// RunPigletPublishCommand handles `pig piglet publish`.
func RunPigletPublishCommand(args []string, stdout, stderr io.Writer) int {
	return runPublish(context.Background(), args, stdout, stderr, configuredBuilders)
}

type publishRequest struct {
	pigletRef   string
	destination string
	repo        string
	signKeyPath string
	targets     string
	artifacts   string
	builder     string
	commit      string
	sourceRef   string
	workspace   string
	yes         bool
	dryRun      bool
}

// pig additive (D18): Stock Pig publishes signed Piglet Binary releases to
// GitHub through the user's GitHub CLI and never contacts a PiG or Pi service.
func runPublish(ctx context.Context, args []string, stdout, stderr io.Writer, builders func() ([]BuilderBackend, error)) int {
	request, help, err := parsePublishArgs(args)
	if err == nil && !help {
		err = request.validate()
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n%s\n", err, publishUsageLine)
		return 2
	}
	if help {
		_, _ = io.WriteString(stdout, publishUsage)
		return 0
	}
	if err := publishGitHub(ctx, request, stdout, stderr, builders); err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func parsePublishArgs(args []string) (publishRequest, bool, error) {
	request := publishRequest{builder: "auto"}
	values := map[string]*string{
		"--to": &request.destination, "--repo": &request.repo, "--sign-key": &request.signKeyPath,
		"--targets": &request.targets, "--artifacts": &request.artifacts, "--builder": &request.builder,
		"--commit": &request.commit, "--source-ref": &request.sourceRef, "--workspace": &request.workspace,
	}
	help := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, inline, hasInline := strings.Cut(arg, "=")
		switch {
		case arg == "-h" || arg == "--help":
			help = true
		case arg == "--yes":
			request.yes = true
		case arg == "--dry-run":
			request.dryRun = true
		case arg == "--no-input":
		case values[name] != nil:
			if !hasInline {
				if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
					return publishRequest{}, false, fmt.Errorf("%s requires a value", name)
				}
				i++
				inline = args[i]
			}
			if inline == "" {
				return publishRequest{}, false, fmt.Errorf("%s requires a value", name)
			}
			*values[name] = inline
		case strings.HasPrefix(arg, "-"):
			return publishRequest{}, false, fmt.Errorf("unknown option %q", arg)
		case request.pigletRef == "":
			request.pigletRef = arg
		default:
			return publishRequest{}, false, fmt.Errorf("unexpected argument %q", arg)
		}
	}
	return request, help, nil
}

func (r publishRequest) validate() error {
	switch {
	case r.pigletRef == "":
		return fmt.Errorf("Piglet name or path is required")
	case r.destination == "":
		return fmt.Errorf("--to is required")
	case r.destination != "github":
		return fmt.Errorf("unsupported Piglet publish destination %q; use --to github", r.destination)
	case r.yes && r.dryRun:
		return fmt.Errorf("--yes and --dry-run cannot be combined")
	case r.repo == "":
		return fmt.Errorf("--to github requires --repo <owner/repo>")
	case !pigletrelease.ValidGitHubRepository(r.repo):
		return fmt.Errorf("--repo %q must be <owner>/<repo>", r.repo)
	case r.signKeyPath == "":
		return fmt.Errorf("--to github requires --sign-key; every Binary and the release index are signed")
	case r.commit != "" && !publishCommitPattern.MatchString(r.commit):
		return fmt.Errorf("--commit must be a full lowercase hexadecimal commit SHA")
	case r.artifacts != "" && r.builder != "auto":
		return fmt.Errorf("--artifacts publishes prebuilt Binaries and cannot be combined with --builder")
	}
	return nil
}

// publishRelease is one validated Piglet release and its signing identity.
type publishRelease struct {
	pigletRef    string
	piglet       *piglet.Piglet
	version      string
	repo         string
	commit       string
	sourceRef    string
	workspace    string
	signKeyPath  string
	key          ed25519.PrivateKey
	keyID        string
	sourceDigest string
	// targets is nil when neither --targets nor build.targets selects any.
	targets []Target
}

// publishAsset is one target's Binary, produced by a builder or collected
// from --artifacts.
type publishAsset struct {
	target   Target
	name     string
	builder  string
	source   string
	manifest signature.Manifest
}

func (r publishRelease) tag() string { return "v" + r.version }

func publishGitHub(ctx context.Context, request publishRequest, stdout, stderr io.Writer, builders func() ([]BuilderBackend, error)) error {
	release, err := preparePublishRelease(request)
	if err != nil {
		return err
	}
	var assets []publishAsset
	if request.artifacts != "" {
		assets, err = collectPublishArtifacts(release, request.artifacts)
	} else {
		assets, err = planPublishBuilds(ctx, release, request.builder, builders)
	}
	if err != nil {
		return err
	}
	if piglet.DistributionOffline() {
		return fmt.Errorf("cannot publish Piglet %q to GitHub while offline; unset PIG_OFFLINE and PI_OFFLINE to run gh", release.piglet.Name)
	}
	exists, err := githubReleaseExists(ctx, release.repo, release.tag())
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("GitHub release %s already exists in %s; published Piglet releases are immutable", release.tag(), release.repo)
	}
	renderPublishPlan(stdout, release, assets, !request.yes)
	if !request.yes {
		return nil
	}
	stage, err := os.MkdirTemp("", "pig-piglet-release-*")
	if err != nil {
		return fmt.Errorf("create release staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := stagePublishAssets(ctx, release, assets, stage, stdout, stderr, builders); err != nil {
		return err
	}
	if err := writeReleaseIndex(release, assets, stage); err != nil {
		return err
	}
	return uploadGitHubRelease(ctx, release, assets, stage, stdout, stderr)
}

func preparePublishRelease(request publishRequest) (publishRelease, error) {
	key, err := signature.ReadPrivateKey(request.signKeyPath)
	if err != nil {
		return publishRelease{}, fmt.Errorf("--sign-key: %w", err)
	}
	workspace := request.workspace
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	p, err := loadPiglet(request.pigletRef, workspace)
	if err != nil {
		return publishRelease{}, err
	}
	if p.Release == nil || p.Release.Version == "" {
		return publishRelease{}, fmt.Errorf("Piglet %q release.version is required for publication", p.Name)
	}
	if err := p.ValidateAgentEnvironmentRuntime(); err != nil {
		return publishRelease{}, err
	}
	sourceDigest, _, err := hashFile(p.SourcePath())
	if err != nil {
		return publishRelease{}, fmt.Errorf("hash Piglet source: %w", err)
	}
	release := publishRelease{
		pigletRef: request.pigletRef, piglet: p, version: p.Release.Version, repo: request.repo,
		commit: request.commit, workspace: workspace, signKeyPath: request.signKeyPath, key: key,
		keyID: signature.KeyID(key.Public().(ed25519.PublicKey)), sourceDigest: sourceDigest,
	}
	if release.sourceRef, err = publishSourceRef(request, release.version); err != nil {
		return publishRelease{}, err
	}
	if release.targets, err = publishTargets(request.targets, p); err != nil {
		return publishRelease{}, err
	}
	return release, nil
}

// publishSourceRef is the Piglet source named by the signed index. By default
// it is the Git commit or release tag the GitHub Release is created at.
func publishSourceRef(request publishRequest, version string) (string, error) {
	if request.sourceRef == "" {
		selector := "v" + version
		if request.commit != "" {
			selector = request.commit
		}
		return "git:github.com/" + request.repo + "@" + selector, nil
	}
	if err := piglet.ValidateRemoteSource(request.sourceRef); err != nil {
		return "", fmt.Errorf("--source-ref: %w", err)
	}
	return request.sourceRef, nil
}

func publishTargets(spec string, p *piglet.Piglet) ([]Target, error) {
	var values []string
	switch {
	case spec != "":
		values = strings.Split(spec, ",")
	case p.Build != nil:
		values = p.Build.Targets
	}
	targets := make([]Target, 0, len(values))
	for _, value := range values {
		target, err := parsePublishTarget(strings.TrimSpace(value))
		if err != nil {
			return nil, err
		}
		if slices.Contains(targets, target) {
			return nil, fmt.Errorf("target %s is listed more than once", target)
		}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return nil, nil
	}
	return targets, nil
}

func parsePublishTarget(value string) (Target, error) {
	goos, goarch, ok := strings.Cut(value, "/")
	if !ok || !publishTargetPartPattern.MatchString(goos) || !publishTargetPartPattern.MatchString(goarch) {
		return Target{}, fmt.Errorf("target %q must be os/arch", value)
	}
	return Target{OS: goos, Arch: goarch}, nil
}

func (r publishRelease) buildOptions(target Target, builder string) Options {
	return Options{
		Format: "binary", Targets: []Target{target}, Sandbox: Sandbox{Native: Target{OS: runtime.GOOS, Arch: runtime.GOARCH}},
		Version: r.version, Builder: builder, Verification: "basic", Workspace: r.workspace,
		SignKeyPath: r.signKeyPath, SignKey: r.key,
	}
}

// planPublishBuilds selects a ready builder for every target before anything
// is built, so a missing target fails the publication without side effects.
func planPublishBuilds(ctx context.Context, release publishRelease, selector string, builders func() ([]BuilderBackend, error)) ([]publishAsset, error) {
	backends, err := builders()
	if err != nil {
		return nil, err
	}
	targets := release.targets
	if targets == nil {
		targets = []Target{{OS: runtime.GOOS, Arch: runtime.GOARCH}}
	}
	missing := map[Target]string{}
	assets := make([]publishAsset, 0, len(targets))
	for _, target := range targets {
		request := BuilderRequest{Piglet: release.piglet, Options: release.buildOptions(target, selector)}
		builder, _, err := selectBuilder(ctx, backends, selector, request)
		if err != nil {
			missing[target] = err.Error()
			continue
		}
		assets = append(assets, publishAsset{target: target, name: pigletrelease.AssetName(release.piglet.Name, target.String()), builder: builder.Name()})
	}
	if len(missing) > 0 {
		return nil, missingPublishTargets(release, missing)
	}
	return sortedAssets(assets), nil
}

// collectPublishArtifacts identifies every file in dir by its signed manifest
// and requires exactly one Binary for each requested target.
func collectPublishArtifacts(release publishRelease, dir string) ([]publishAsset, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("--artifacts: %w", err)
	}
	found := map[Target]publishAsset{}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if !entry.Type().IsRegular() {
			return nil, fmt.Errorf("--artifacts %s contains %s, which is not a regular file; the directory must hold only signed Piglet Binaries", dir, entry.Name())
		}
		manifest, err := release.verifyBinary(path, nil)
		if err != nil {
			return nil, err
		}
		target, _ := parsePublishTarget(manifest.Target)
		if prior, duplicate := found[target]; duplicate {
			return nil, fmt.Errorf("--artifacts holds two Piglet Binaries for %s: %s and %s", target, prior.source, path)
		}
		found[target] = publishAsset{target: target, name: pigletrelease.AssetName(release.piglet.Name, target.String()), source: path, manifest: manifest}
	}
	targets := release.targets
	if targets == nil {
		targets = slices.SortedFunc(maps.Keys(found), compareTargets)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("--artifacts %s holds no Piglet Binaries", dir)
	}
	for target, asset := range found {
		if !slices.Contains(targets, target) {
			return nil, fmt.Errorf("--artifacts holds %s for %s, which is not a requested target", asset.source, target)
		}
	}
	missing := map[Target]string{}
	assets := make([]publishAsset, 0, len(targets))
	for _, target := range targets {
		asset, ok := found[target]
		if !ok {
			missing[target] = "no signed Piglet Binary for this target in " + dir
			continue
		}
		assets = append(assets, asset)
	}
	if len(missing) > 0 {
		return nil, missingPublishTargets(release, missing)
	}
	return sortedAssets(assets), nil
}

func missingPublishTargets(release publishRelease, missing map[Target]string) error {
	var message strings.Builder
	_, _ = fmt.Fprintf(&message, "Piglet %s %s has no signed Piglet Binary for %d target(s); nothing was built or uploaded:", release.piglet.Name, release.version, len(missing))
	for _, target := range slices.SortedFunc(maps.Keys(missing), compareTargets) {
		_, _ = fmt.Fprintf(&message, "\n  %s: %s", target, missing[target])
	}
	_, _ = fmt.Fprintf(&message, "\nBuild each missing target on a matching host with `pig piglet build %s --format binary --sign-key <key>`, collect the Binaries in one directory, and publish them with --artifacts <dir>.", release.pigletRef)
	return errors.New(message.String())
}

func compareTargets(a, b Target) int {
	return strings.Compare(a.String(), b.String())
}

func sortedAssets(assets []publishAsset) []publishAsset {
	slices.SortFunc(assets, func(a, b publishAsset) int { return strings.Compare(a.name, b.name) })
	return assets
}

// verifyBinary checks that path is a Binary of this release: validly signed by
// the release key and built from this Piglet source and release.version.
func (r publishRelease) verifyBinary(path string, want *Target) (signature.Manifest, error) {
	status, err := signature.Check(path, signature.Policy{})
	if err != nil {
		return signature.Manifest{}, fmt.Errorf("Piglet Binary %s: %w", path, err)
	}
	if !status.Signed {
		return signature.Manifest{}, fmt.Errorf("%s is not a signed Piglet Binary; build it with --sign-key", path)
	}
	if status.KeyID != r.keyID {
		return signature.Manifest{}, fmt.Errorf("Piglet Binary %s is signed by %s; --sign-key is %s", path, status.KeyID, r.keyID)
	}
	manifest := status.Manifest
	for _, check := range []struct{ name, signed, want string }{
		{"Piglet", manifest.Piglet, r.piglet.Name},
		{"release version", manifest.ReleaseVersion, r.version},
		{"Piglet source digest", manifest.SourceDigest, r.sourceDigest},
	} {
		if check.signed != check.want {
			return signature.Manifest{}, fmt.Errorf("Piglet Binary %s names %s %q; this release has %q", path, check.name, check.signed, check.want)
		}
	}
	target, err := parsePublishTarget(manifest.Target)
	if err != nil {
		return signature.Manifest{}, fmt.Errorf("Piglet Binary %s: %w", path, err)
	}
	if want != nil && target != *want {
		return signature.Manifest{}, fmt.Errorf("Piglet Binary %s is built for %s, want %s", path, target, *want)
	}
	return manifest, nil
}

func renderPublishPlan(w io.Writer, release publishRelease, assets []publishAsset, dryRun bool) {
	title := "Piglet GitHub release"
	if dryRun {
		title += " dry run"
	}
	_, _ = fmt.Fprintf(w, "%s\nRelease: %s %s\nDestination: github.com/%s (tag %s)\nSigner: %s\nSource: %s\nAssets:\n",
		title, release.piglet.Name, release.version, release.repo, release.tag(), release.keyID, release.sourceRef)
	nameWidth, targetWidth := len("piglet-release.json"), 0
	for _, asset := range assets {
		nameWidth = max(nameWidth, len(asset.name))
		targetWidth = max(targetWidth, len(asset.target.String()))
	}
	for _, asset := range assets {
		origin := "build with " + asset.builder
		if asset.source != "" {
			origin = "from " + asset.source
		}
		_, _ = fmt.Fprintf(w, "  %-*s  %-*s  %s\n", nameWidth, asset.name, targetWidth, asset.target, origin)
	}
	_, _ = fmt.Fprintf(w, "  SHA256SUMS\n  piglet-release.json\nPull with: pig piglet pull github:%s@%s\n", release.repo, release.version)
	if dryRun {
		_, _ = fmt.Fprintln(w, "Dry run only; rerun with --yes to build, sign, and upload.")
	}
}

// stagePublishAssets writes every target's Binary into stage under its release
// asset name and verifies the staged bytes that will be uploaded.
func stagePublishAssets(ctx context.Context, release publishRelease, assets []publishAsset, stage string, stdout, stderr io.Writer, builders func() ([]BuilderBackend, error)) error {
	var build func(publishAsset, string) error
	if slices.ContainsFunc(assets, func(asset publishAsset) bool { return asset.source == "" }) {
		var err error
		if build, err = release.prepareBuilds(ctx, stdout, stderr, builders); err != nil {
			return err
		}
	}
	pigVersions := map[string][]Target{}
	for i := range assets {
		asset := &assets[i]
		path := filepath.Join(stage, asset.name)
		var err error
		if asset.source != "" {
			err = copyReleaseAsset(asset.source, path)
		} else {
			err = build(*asset, path)
		}
		if err != nil {
			return err
		}
		if asset.manifest, err = release.verifyBinary(path, &asset.target); err != nil {
			return err
		}
		pigVersions[asset.manifest.PigVersion] = append(pigVersions[asset.manifest.PigVersion], asset.target)
	}
	if len(pigVersions) > 1 {
		return fmt.Errorf("the Piglet Binaries were built by different PiG versions %v; build every target with one PiG version", pigVersions)
	}
	return nil
}

// prepareBuilds resolves the Piglet's build closure once and returns a
// function that builds one target with its planned builder.
func (r publishRelease) prepareBuilds(ctx context.Context, stdout, stderr io.Writer, builders func() ([]BuilderBackend, error)) (func(publishAsset, string) error, error) {
	backends, err := builders()
	if err != nil {
		return nil, err
	}
	cells, warnings := resolvePigletCells(r.piglet)
	exts := extensionInputsFromCells(cells)
	requireFused := r.piglet.Build != nil && r.piglet.Build.ExtensionRealization == "fused"
	if verdict := Validate(BuildPlan(exts, Options{}), warnings, len(exts), requireFused); !verdict.OK {
		return nil, fmt.Errorf("Piglet will not build: %s", strings.Join(verdict.Blockers, "; "))
	}
	var baked Options
	if err := applyBakedPiglet(r.pigletRef, r.piglet, &baked); err != nil {
		return nil, err
	}
	return func(asset publishAsset, output string) error {
		options := r.buildOptions(asset.target, asset.builder)
		options.BakedSettings = baked.BakedSettings
		_, err := executeBuild(ctx, backends, asset.builder, BuilderRequest{
			Piglet: r.piglet, Cells: cells, Options: options, Output: output, Stdout: stdout, Stderr: stderr,
		})
		if err != nil {
			return fmt.Errorf("build %s for %s: %w", r.piglet.Name, asset.target, err)
		}
		return nil
	}, nil
}

func copyReleaseAsset(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }() // Read-only: close cannot lose data.
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("stage %s: %w", source, err)
	}
	return output.Close()
}

// writeReleaseIndex writes SHA256SUMS and the signed piglet-release.json, then
// checks every staged asset against the signed index exactly as pull will.
func writeReleaseIndex(release publishRelease, assets []publishAsset, stage string) error {
	index := pigletrelease.Index{
		Piglet: release.piglet.Name, Version: release.version, PigVersion: assets[0].manifest.PigVersion,
		SourceRef: release.sourceRef, Binaries: make(map[string]pigletrelease.Binary, len(assets)),
	}
	var sums strings.Builder
	for _, asset := range assets {
		digest, size, err := hashFile(filepath.Join(stage, asset.name))
		if err != nil {
			return err
		}
		sha := strings.TrimPrefix(digest, "sha256:")
		index.Binaries[asset.target.String()] = pigletrelease.Binary{URL: asset.name, SHA256: sha, Size: size}
		_, _ = fmt.Fprintf(&sums, "%s  %s\n", sha, asset.name)
	}
	indexData, err := pigletrelease.Sign(index, release.key)
	if err != nil {
		return fmt.Errorf("sign Piglet release index: %w", err)
	}
	verified, err := pigletrelease.Verify(indexData)
	if err != nil {
		return err
	}
	for _, asset := range assets {
		if err := pigletrelease.VerifyAsset(filepath.Join(stage, asset.name), verified, asset.target.String()); err != nil {
			return fmt.Errorf("staged release asset %s would fail pull verification: %w", asset.name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(stage, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stage, "piglet-release.json"), indexData, 0o644)
}

func githubReleaseExists(ctx context.Context, repo, tag string) (bool, error) {
	command := exec.CommandContext(ctx, "gh", "release", "view", tag, "--repo", repo, "--json", "tagName")
	command.Env = ghEnvironment()
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return true, nil
	}
	if _, ok := errors.AsType[*exec.ExitError](err); ok && strings.Contains(stderr.String(), "release not found") {
		return false, nil
	}
	return false, ghError(fmt.Sprintf("check GitHub release %s in %s", tag, repo), err, stderr.String())
}

func uploadGitHubRelease(ctx context.Context, release publishRelease, assets []publishAsset, stage string, stdout, stderr io.Writer) error {
	args := []string{"release", "create", release.tag()}
	targets := make([]string, 0, len(assets))
	for _, asset := range assets {
		args = append(args, asset.name)
		targets = append(targets, asset.target.String())
	}
	notes := fmt.Sprintf("Signed Piglet Binary release for %s %s.\n\nInstall it with PiG:\n\n    pig piglet pull github:%s@%s\n\nSigner: %s\nTargets: %s\nSource: %s\n\nEach Binary carries an Ed25519 signature trailer. piglet-release.json is the DSSE-signed release index, and SHA256SUMS lists the SHA-256 of each Binary.\n",
		release.piglet.Name, release.version, release.repo, release.version, release.keyID, strings.Join(targets, ", "), release.sourceRef)
	args = append(args, "SHA256SUMS", "piglet-release.json", "--repo", release.repo, "--title", release.piglet.Name+" "+release.tag(), "--notes", notes)
	if release.commit != "" {
		args = append(args, "--target", release.commit)
	}
	if semver.Prerelease("v"+release.version) != "" {
		args = append(args, "--prerelease")
	}
	command := exec.CommandContext(ctx, "gh", args...)
	command.Dir = stage
	command.Env = ghEnvironment()
	command.Stdout = stdout
	var ghStderr bytes.Buffer
	command.Stderr = &ghStderr
	if err := command.Run(); err != nil {
		return ghError("create GitHub release "+release.tag()+" in "+release.repo, err, ghStderr.String())
	}
	_, _ = stderr.Write(ghStderr.Bytes())
	_, _ = fmt.Fprintf(stdout, "Published %s %s to https://github.com/%s/releases/tag/%s\n", release.piglet.Name, release.version, release.repo, release.tag())
	return nil
}

func ghEnvironment() []string {
	return append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
}

func ghError(action string, err error, stderr string) error {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s: GitHub publication runs the GitHub CLI (gh), which is not on PATH; install gh and run `gh auth login`", action)
	}
	if message := strings.TrimSpace(stderr); message != "" {
		return fmt.Errorf("%s: %w: %s", action, err, message)
	}
	return fmt.Errorf("%s: %w", action, err)
}
