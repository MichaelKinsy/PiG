package pigletbuild

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	pigletartifact "github.com/MichaelKinsy/PiG/coding/piglet/artifact"
	"github.com/MichaelKinsy/PiG/internal/buildprogress"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

var (
	containerBuilderNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	containerImagePattern       = regexp.MustCompile(`^[^@\s]+@sha256:[0-9a-f]{64}$`)
)

type ContainerBuilderConfig struct {
	Name   string `json:"name"`
	Engine string `json:"engine"`
	Image  string `json:"image"`
}

type containerBuilderConfigFile struct {
	Builders []ContainerBuilderConfig `json:"builders"`
}

type containerCommandRunner func(context.Context, string, ...string) ([]byte, error)
type executableLookup func(string) (string, error)

type containerBuilder struct {
	config   ContainerBuilderConfig
	lookPath executableLookup
	run      containerCommandRunner
}

func (b containerBuilder) Name() string { return b.config.Name }

func (b containerBuilder) Probe(ctx context.Context, request BuilderRequest) BuilderReadiness {
	result := BuilderReadiness{Builder: b.Name()}
	if err := ctx.Err(); err != nil {
		result.Code = "cancelled"
		result.Message = err.Error()
		return result
	}
	if request.Options.SignKeyPath != "" {
		result.Code = "signing-unsupported"
		result.Message = "the container builder does not sign Piglet Binaries; the signing key never enters the container"
		result.Remedy = "sign with --builder native, or attest the CI build with keyless Sigstore and check it with pig verify --provenance"
		return result
	}
	if len(request.Options.Targets) != 1 || request.Options.Targets[0].OS != "linux" {
		result.Code = "target-unavailable"
		result.Message = fmt.Sprintf("container builder supports exactly one Linux target; requested %v", request.Options.Targets)
		result.Remedy = "select one linux/<arch> target or use another builder"
		return result
	}
	engine, err := b.resolveEngine()
	if err != nil {
		result.Code = "engine-unavailable"
		result.Message = err.Error()
		result.Remedy = "install and start Docker or Podman as configured"
		return result
	}
	if _, err := b.runCommand(ctx, engine, "info"); err != nil {
		result.Code = "engine-unavailable"
		result.Message = strings.TrimSpace(err.Error())
		result.Remedy = "start the configured container engine"
		return result
	}
	imageMetadata, err := b.runCommand(ctx, engine, "image", "inspect", "--format", `{{.Os}}/{{.Architecture}}`, b.config.Image)
	if err != nil {
		result.Code = "image-unavailable"
		result.Message = fmt.Sprintf("digest-pinned builder image %s is not present locally", b.config.Image)
		result.Remedy = fmt.Sprintf("authenticate with %s outside Pig if required, then run `%s pull %s`", imageRegistry(b.config.Image), engine, b.config.Image)
		return result
	}
	metadata := strings.Fields(string(imageMetadata))
	requestedTarget := request.Options.Targets[0].String()
	if len(metadata) == 0 || metadata[0] != requestedTarget {
		platform := "unknown"
		if len(metadata) > 0 {
			platform = metadata[0]
		}
		result.Code = "image-platform-unavailable"
		result.Message = fmt.Sprintf("local builder image platform is %s, requested %s", platform, requestedTarget)
		result.Remedy = fmt.Sprintf("run `%s pull --platform %s %s`", engine, requestedTarget, b.config.Image)
		return result
	}
	result.Ready = true
	return result
}

func (b containerBuilder) Build(ctx context.Context, request BuilderRequest) (BuilderResult, error) {
	// pig additive (D18): the container builder reports its own work without inventing inner toolchain phases.
	buildprogress.Phase(ctx, "Preparing container build", b.Name())
	engine, err := b.resolveEngine()
	if err != nil {
		return BuilderResult{}, err
	}
	if len(request.Options.Targets) != 1 || request.Options.Targets[0].OS != "linux" {
		return BuilderResult{}, fmt.Errorf("container builder supports exactly one Linux target; requested %v", request.Options.Targets)
	}
	target := request.Options.Targets[0]
	request.Options.Sandbox.Native = target
	artifactPath, err := containerArtifactPath(request)
	if err != nil {
		return BuilderResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		return BuilderResult{}, err
	}
	buildRoot, err := os.MkdirTemp(filepath.Dir(artifactPath), ".pig-container-build-*")
	if err != nil {
		return BuilderResult{}, err
	}
	stagedRoot := buildRoot
	defer func() { _ = os.RemoveAll(stagedRoot) }()
	resolvedBuildRoot, err := filepath.EvalSymlinks(buildRoot)
	if err != nil {
		return BuilderResult{}, fmt.Errorf("resolve container build directory: %w", err)
	}
	buildRoot = resolvedBuildRoot
	inputDir := filepath.Join(buildRoot, "input")
	outputDir := filepath.Join(buildRoot, "output")
	recordHome := filepath.Join(buildRoot, "records-home")
	for _, dir := range []string{inputDir, outputDir, recordHome} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return BuilderResult{}, err
		}
	}
	pigletData, mounts, err := localizeContainerPiglet(request.Piglet)
	if err != nil {
		return BuilderResult{}, err
	}
	pigletPath := filepath.Join(inputDir, "piglet.yaml")
	if err := os.WriteFile(pigletPath, pigletData, 0o644); err != nil {
		return BuilderResult{}, err
	}
	userArgs, err := containerUserArgs()
	if err != nil {
		return BuilderResult{}, err
	}
	containerArtifact := filepath.Join(outputDir, filepath.Base(artifactPath))
	args := append([]string{"run", "--rm", "--pull=never", "--platform", target.String()}, userArgs...)
	args = append(args,
		"--mount", containerMount(outputDir, "/out", false),
		"--mount", containerMount(inputDir, "/input", true),
		"--mount", containerMount(recordHome, "/records-home", false),
		"--env", "HOME=/records-home", "--env", "PIG_HOME=/records-home", "--env", "PIG_CODING_AGENT_DIR=/records-home/agent",
		"--env", "GIT_CONFIG_COUNT=1", "--env", "GIT_CONFIG_KEY_0=safe.directory", "--env", "GIT_CONFIG_VALUE_0=*",
	)
	for _, mount := range mounts {
		args = append(args, "--mount", containerMount(mount.Source, mount.Target, true))
	}
	args = append(args, "--entrypoint", "pig", b.config.Image, "piglet", "build", "/input/piglet.yaml", "--format", "binary", "--builder", "native", "--verification", "basic", "--no-input", "--out", "/out/"+filepath.Base(artifactPath), "--targets", target.String())
	if buildprogress.Verbose(ctx) {
		args = append(args, "--verbose")
	}
	buildprogress.Phase(ctx, "Building in container", b.Name()+" · "+target.String())
	if _, err := b.runCommandWithStreams(buildprogress.Member(ctx, b.Name()), engine, request.Stdout, request.Stderr, args...); err != nil {
		return BuilderResult{}, err
	}
	buildprogress.Phase(ctx, "Verifying container artifact", artifactPath)
	innerRecords, err := readBuiltRecords(filepath.Join(recordHome, "receipts", "piglets"))
	if err != nil {
		return BuilderResult{}, fmt.Errorf("read container build records: %w", err)
	}
	portable, err := buildPortableLock(request.Piglet, request.Cells, request.Options)
	if err != nil {
		return BuilderResult{}, err
	}
	innerResolution := innerRecords.Resolution.Resolution
	innerBinary := innerRecords.Binary.Binary
	if innerResolution == nil || innerBinary == nil || innerResolution.EffectiveDigest != portable.ResolutionRecord.Resolution.EffectiveDigest || innerResolution.ComponentPlan.Digest != portable.ComponentPlan.Digest || innerBinary.Target != target.String() {
		return BuilderResult{}, fmt.Errorf("container builder records disagree with the locked runtime Piglet, target, or component plan")
	}
	artifactDigest, artifactSize, err := hashFile(containerArtifact)
	if err != nil {
		return BuilderResult{}, fmt.Errorf("hash container artifact: %w", err)
	}
	if artifactDigest != innerBinary.Artifact.Digest || artifactSize != innerBinary.Artifact.Size {
		return BuilderResult{}, fmt.Errorf("container artifact does not match its Piglet Binary record")
	}
	if err := copyContainerArtifact(containerArtifact, artifactPath); err != nil {
		return BuilderResult{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(artifactPath)
		}
	}()
	fileName := "pig-" + request.Piglet.Name
	if request.Piglet.Build != nil && request.Piglet.Build.OutputName != "" {
		fileName = request.Piglet.Build.OutputName
	}
	binaryRecord, err := pigletartifact.NewBinaryRecord(portable.Piglet, portable.ReleaseVersion, time.Now(), portable.ResolutionRecord, pigletartifact.BinaryInput{
		Target: target.String(), PigVersion: innerBinary.PigVersion,
		PigSourceRevision: innerBinary.PigSourceRevision, PigSourceDigest: innerBinary.PigSourceDigest,
		Builder: b.Name(), BuilderIdentity: "container:" + engine + ":" + b.config.Image, Toolchains: innerBinary.Toolchains,
		Artifact:     pigletartifact.Artifact{Digest: artifactDigest, Size: artifactSize, FileName: fileName},
		Verification: innerBinary.Verification,
	})
	if err != nil {
		return BuilderResult{}, fmt.Errorf("build container Piglet Binary record: %w", err)
	}
	buildprogress.Phase(ctx, "Writing binary and records", artifactPath)
	recordPath, err := writeBinaryRecords(binaryBuildRecords{Resolution: portable.ResolutionRecord, Binary: binaryRecord}, artifactPath)
	if err != nil {
		return BuilderResult{}, err
	}
	cleanup = false
	if request.Stdout != nil && !buildprogress.Enabled(ctx) {
		_, _ = fmt.Fprintf(request.Stdout, "Piglet Binary record: %s\n", recordPath)
	}
	return BuilderResult{Builder: b.Name(), Artifact: artifactPath, Record: recordPath}, nil
}

func readBuiltRecords(root string) (binaryBuildRecords, error) {
	var records binaryBuildRecords
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		record, err := pigletartifact.ParseRecord(data)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		switch record.Kind {
		case pigletartifact.RecordKindResolution:
			if records.Resolution.Kind != "" {
				return fmt.Errorf("multiple piglet-resolution records produced")
			}
			records.Resolution = record
		case pigletartifact.RecordKindBinary:
			if records.Binary.Kind != "" {
				return fmt.Errorf("multiple piglet-binary records produced")
			}
			records.Binary = record
		default:
			return fmt.Errorf("unexpected Piglet record kind %q", record.Kind)
		}
		return nil
	})
	if err != nil {
		return binaryBuildRecords{}, err
	}
	if records.Resolution.Kind == "" || records.Binary.Kind == "" {
		return binaryBuildRecords{}, fmt.Errorf("container build did not produce both Piglet resolution and Binary records")
	}
	if err := pigletartifact.ValidateBinaryLink(records.Resolution, records.Binary); err != nil {
		return binaryBuildRecords{}, err
	}
	return records, nil
}

func (b containerBuilder) resolveEngine() (string, error) {
	lookPath := b.lookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if b.config.Engine != "auto" {
		if _, err := lookPath(b.config.Engine); err != nil {
			return "", fmt.Errorf("%s executable: %w", b.config.Engine, err)
		}
		return b.config.Engine, nil
	}
	for _, name := range []string{"docker", "podman"} {
		if _, err := lookPath(name); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("neither docker nor podman is on PATH")
}

func (b containerBuilder) runCommand(ctx context.Context, engine string, args ...string) ([]byte, error) {
	if b.run != nil {
		return b.run(ctx, engine, args...)
	}
	command := exec.CommandContext(ctx, engine, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("%s %s: %s", engine, strings.Join(args, " "), message)
	}
	return stdout.Bytes(), nil
}

func (b containerBuilder) runCommandWithStreams(ctx context.Context, engine string, stdout, stderr io.Writer, args ...string) ([]byte, error) {
	if b.run != nil {
		output, err := b.run(ctx, engine, args...)
		if len(output) > 0 && stdout != nil {
			_, _ = stdout.Write(output)
		}
		return output, err
	}
	command := exec.CommandContext(ctx, engine, args...)
	if buildprogress.Enabled(ctx) {
		if err := buildprogress.Run(ctx, command); err != nil {
			return nil, fmt.Errorf("%s build: %w", engine, err)
		}
		return nil, nil
	}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %w", engine, strings.Join(args, " "), err)
	}
	return nil, nil
}

func containerArtifactPath(request BuilderRequest) (string, error) {
	output := request.Output
	if output == "" {
		output = "pig-" + request.Piglet.Name
	}
	artifactPath, err := filepath.Abs(output)
	if err != nil {
		return "", err
	}
	if !artifactNamePattern.MatchString(filepath.Base(artifactPath)) {
		return "", fmt.Errorf("artifact basename %q contains unsupported characters", filepath.Base(artifactPath))
	}
	if _, err := os.Stat(artifactPath); err == nil {
		return "", fmt.Errorf("artifact %s already exists; choose a different --out path or remove it explicitly", artifactPath)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return artifactPath, nil
}

type containerPigletMount struct {
	Source string
	Target string
}

func localizeContainerPiglet(p *piglet.Piglet) ([]byte, []containerPigletMount, error) {
	localized := piglet.Clone(p)
	localized.Build = nil
	localized.Packages = nil
	mountSet := make(map[string]string)
	var err error
	resolvedExtensions, extensionErrs := piglet.ResolveExtensions(p)
	if len(extensionErrs) > 0 {
		return nil, nil, extensionErrs[0]
	}
	extensionsByName := make(map[string]string, len(resolvedExtensions))
	for _, resolved := range resolvedExtensions {
		extensionsByName[resolved.Entry.Name] = resolved.Path
	}
	for i := range localized.Extensions {
		if len(localized.Extensions[i].Origins) == 0 {
			continue
		}
		path := extensionsByName[localized.Extensions[i].Name]
		if path == "" {
			return nil, nil, fmt.Errorf("container build extension %q has no resolved path", localized.Extensions[i].Name)
		}
		path, err = canonicalContainerInput(path)
		if err != nil {
			return nil, nil, err
		}
		replacements, err := localGoReplacementDirs(path)
		if err != nil {
			return nil, nil, fmt.Errorf("container build extension %q local Go replacements: %w", localized.Extensions[i].Name, err)
		}
		inputs := append([]string{path}, replacements...)
		root, err := commonContainerInputRoot(inputs)
		if err != nil {
			return nil, nil, err
		}
		targetRoot := containerJoin("/input/sources/extensions", localized.Extensions[i].Name)
		for _, input := range inputs {
			input, err = canonicalContainerInput(input)
			if err != nil {
				return nil, nil, err
			}
			relative, err := filepath.Rel(root, input)
			if err != nil {
				return nil, nil, err
			}
			target := targetRoot
			if relative != "." {
				target = containerJoin(targetRoot, filepath.ToSlash(relative))
			}
			mountSet[target] = input
			if input == path {
				localized.Extensions[i].Origins = []string{"local:./" + strings.TrimPrefix(target, "/input/")}
			}
		}
	}
	resolvedSkills, skillErrs := piglet.ResolveSkills(p)
	if len(skillErrs) > 0 {
		return nil, nil, skillErrs[0]
	}
	skillsByName := make(map[string]string, len(resolvedSkills))
	for _, resolved := range resolvedSkills {
		skillsByName[resolved.Entry.Name] = resolved.Path
	}
	for i := range localized.Skills {
		if localized.Skills[i].Content != "" {
			continue
		}
		path := skillsByName[localized.Skills[i].Name]
		if path == "" {
			return nil, nil, fmt.Errorf("container build skill %q has no resolved path", localized.Skills[i].Name)
		}
		path, err = canonicalContainerInput(path)
		if err != nil {
			return nil, nil, err
		}
		target := containerJoin("/input/sources/skills", localized.Skills[i].Name)
		localized.Skills[i].Origins = []string{"local:./" + strings.TrimPrefix(target, "/input/")}
		mountSet[target] = path
	}
	pigletDir := filepath.Dir(p.SourcePath())
	localizePrompt := func(ref *piglet.PromptRef) error {
		if ref == nil || ref.File == "" {
			return nil
		}
		path := ref.File
		if !filepath.IsAbs(path) {
			path = filepath.Join(pigletDir, path)
		}
		path, err = canonicalContainerInput(path)
		if err != nil {
			return err
		}
		target := containerJoin("/input/sources/prompts", filepath.Base(path))
		ref.File = "./" + strings.TrimPrefix(target, "/input/")
		mountSet[target] = path
		return nil
	}
	if err := localizePrompt(localized.SystemPrompt); err != nil {
		return nil, nil, err
	}
	mounts := make([]containerPigletMount, 0, len(mountSet))
	for target, source := range mountSet {
		mounts = append(mounts, containerPigletMount{Source: source, Target: target})
	}
	slices.SortFunc(mounts, func(a, b containerPigletMount) int {
		return strings.Compare(a.Target, b.Target)
	})
	localizedData, err := yaml.Marshal(localized)
	return localizedData, mounts, err
}

// containerJoin joins path elements inside the Linux build container, whose
// separator is / on every host.
func containerJoin(elem ...string) string { return path.Join(elem...) }

func commonContainerInputRoot(paths []string) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("container build input set is empty")
	}
	root := paths[0]
	for _, path := range paths[1:] {
		for {
			relative, err := filepath.Rel(root, path)
			if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				break
			}
			parent := filepath.Dir(root)
			if parent == root {
				return "", fmt.Errorf("container build inputs %q and %q have no common root", paths[0], path)
			}
			root = parent
		}
	}
	return root, nil
}

func canonicalContainerInput(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve container build input %s: %w", path, err)
	}
	if strings.Contains(resolved, ",") {
		return "", fmt.Errorf("container build input path %s contains an unsupported comma", resolved)
	}
	return resolved, nil
}

func containerMount(source, target string, readonly bool) string {
	mount := "type=bind,src=" + source + ",dst=" + target
	if readonly {
		mount += ",readonly"
	}
	return mount
}

// containerUserArgs returns the --user argument the build container runs
// with, so artifacts it writes to the bind-mounted output belong to the
// invoking user.
func containerUserArgs() ([]string, error) {
	return containerUserArgsFor(runtime.GOOS, user.Current, os.Getuid, os.Getgid)
}

// containerUserArgsFor resolves the --user argument on goos. On Windows there
// is no host uid or gid to map: the container runs as the image's default
// user and Docker Desktop's VM owns the bind-mounted files, so neither the
// account nor the numeric ids are looked up.
//
// user.Current needs a passwd entry, which a container running as an arbitrary
// numeric uid does not have. That is the normal case for the environments a
// container build targets, so the kernel's own view of the ids is the fallback.
func containerUserArgsFor(goos string, lookup func() (*user.User, error), getuid, getgid func() int) ([]string, error) {
	// pig divergence (D67): a Windows host maps no uid:gid into the build container.
	if goos == "windows" {
		return nil, nil
	}
	identity, err := containerIdentityFrom(lookup, getuid, getgid)
	if err != nil {
		return nil, err
	}
	return []string{"--user", identity}, nil
}

// containerIdentityFrom resolves the identity from an account lookup, falling
// back to the kernel's numeric ids. Taking the lookup as a parameter is what
// lets a test reach the fallback on a host whose own lookup succeeds.
func containerIdentityFrom(lookup func() (*user.User, error), getuid, getgid func() int) (string, error) {
	if current, err := lookup(); err == nil {
		uid, uidErr := strconv.Atoi(current.Uid)
		gid, gidErr := strconv.Atoi(current.Gid)
		if uidErr == nil && gidErr == nil && uid >= 0 && gid >= 0 {
			return strconv.Itoa(uid) + ":" + strconv.Itoa(gid), nil
		}
	}
	uid, gid := getuid(), getgid()
	if uid < 0 || gid < 0 {
		return "", fmt.Errorf("resolve invoking user: no passwd entry and no numeric uid/gid")
	}
	return strconv.Itoa(uid) + ":" + strconv.Itoa(gid), nil
}

func copyContainerArtifact(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	stage, err := os.CreateTemp(filepath.Dir(target), ".pig-artifact-*.stage")
	if err != nil {
		return err
	}
	stagePath := stage.Name()
	defer func() { _ = os.Remove(stagePath) }()
	if _, err := io.Copy(stage, input); err != nil {
		_ = stage.Close()
		return err
	}
	if err := stage.Chmod(0o755); err != nil {
		_ = stage.Close()
		return err
	}
	if err := stage.Close(); err != nil {
		return err
	}
	return os.Rename(stagePath, target)
}

func configuredBuilders() ([]BuilderBackend, error) {
	configs, err := loadContainerBuilderConfigs()
	if err != nil {
		return nil, err
	}
	builders := make([]BuilderBackend, 0, len(configs)+1)
	for _, config := range configs {
		builders = append(builders, containerBuilder{config: config})
	}
	return append(builders, nativeBuilder{}), nil
}

func loadContainerBuilderConfigs() ([]ContainerBuilderConfig, error) {
	path := strings.TrimSpace(os.Getenv("PIG_BUILDERS_FILE"))
	if path == "" {
		path = filepath.Join(codingagent.StateDir("pigletbuild"), "builders.json")
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read builder configuration %s: %w", path, err)
	}
	var file containerBuilderConfigFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("parse builder configuration %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("parse builder configuration %s: trailing content", path)
	}
	seen := make(map[string]struct{}, len(file.Builders))
	for i := range file.Builders {
		config := &file.Builders[i]
		if !containerBuilderNamePattern.MatchString(config.Name) || config.Name == "native" || config.Name == "auto" {
			return nil, fmt.Errorf("builders[%d].name %q is invalid or reserved", i, config.Name)
		}
		if _, exists := seen[config.Name]; exists {
			return nil, fmt.Errorf("duplicate builder name %q", config.Name)
		}
		seen[config.Name] = struct{}{}
		if config.Engine == "" {
			config.Engine = "auto"
		}
		if config.Engine != "auto" && config.Engine != "docker" && config.Engine != "podman" {
			return nil, fmt.Errorf("builder %q engine must be auto, docker, or podman", config.Name)
		}
		if !containerImagePattern.MatchString(config.Image) || strings.Count(config.Image, "@") != 1 {
			return nil, fmt.Errorf("builder %q image must be digest-pinned as <registry>/<image>@sha256:<64 lowercase hex>", config.Name)
		}
	}
	return file.Builders, nil
}

func imageRegistry(image string) string {
	name, _, _ := strings.Cut(image, "@")
	registry, _, ok := strings.Cut(name, "/")
	if !ok {
		return "the image registry"
	}
	return registry
}
