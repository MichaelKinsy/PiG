package pigletbuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/modfile"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	pigletartifact "github.com/MichaelKinsy/PiG/coding/piglet/artifact"
	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
	sourceref "github.com/MichaelKinsy/PiG/coding/source"
	"github.com/MichaelKinsy/PiG/internal/buildprogress"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

var artifactNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]*$`)

type buildLock struct {
	Piglet            string
	ReleaseVersion    string
	ComponentPlan     pigletartifact.Plan
	ResolutionRecord  pigletartifact.Record
	PigVersion        string
	PigSourceRevision string
	PigSourceDigest   string
	Builder           string
	BuilderIdentity   string
	Target            string
	Toolchains        map[string]string
}

type buildInput struct {
	Kind    string
	Name    string
	Source  string
	Version string
	Package string
	Digest  string
}

type binaryBuildRecords struct {
	Resolution pigletartifact.Record
	Binary     pigletartifact.Record
}

func validateNativeTargets(targets []Target) error {
	host := Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	if len(targets) != 1 || targets[0] != host {
		return fmt.Errorf("native builder supports only host target %s; requested %v. Configure a container or remote builder for other targets", host, targets)
	}
	return nil
}

// nativeArtifactPath returns the absolute path a native build writes. The
// default name is pig-<name>, with .exe for a Windows target as go build names
// its default output. Windows runs only files with an executable extension,
// so an explicit Windows output without one fails before the build instead
// of producing a binary that cannot start.
func nativeArtifactPath(outPath, name string, target Target) (string, error) {
	if outPath == "" {
		outPath = "pig-" + name
		if target.OS == "windows" {
			outPath += ".exe"
		}
	}
	artifactPath, err := filepath.Abs(outPath)
	if err != nil {
		return "", err
	}
	if target.OS == "windows" && filepath.Ext(artifactPath) == "" {
		return "", fmt.Errorf("a Windows Piglet Binary needs an executable extension so Windows can start it; name the output %s.exe", filepath.Base(artifactPath))
	}
	return artifactPath, nil
}

func buildNativeWithRecords(ctx context.Context, p *piglet.Piglet, cells []subprocess.CellSpec, opts Options, outPath string, stdout, stderr io.Writer) (string, string, error) {
	if err := validateNativeTargets(opts.Targets); err != nil {
		return "", "", err
	}
	artifactPath, err := nativeArtifactPath(outPath, p.Name, opts.Targets[0])
	if err != nil {
		return "", "", err
	}
	if !artifactNamePattern.MatchString(filepath.Base(artifactPath)) {
		return "", "", fmt.Errorf("artifact basename %q contains unsupported characters", filepath.Base(artifactPath))
	}
	if _, err := os.Stat(artifactPath); err == nil {
		return "", "", fmt.Errorf("artifact %s already exists; choose a different --out path or remove it explicitly", artifactPath)
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		return "", "", err
	}
	buildprogress.Phase(ctx, "Locking build inputs", "Hashing source, extensions, and toolchain identities")
	lock, err := buildNativeLock(p, cells, opts)
	if err != nil {
		return "", "", err
	}
	embedded, err := buildNativeArtifact(ctx, p, cells, lock.ComponentPlan, &lock.ResolutionRecord, opts, artifactPath, stdout, stderr)
	if err != nil {
		return "", "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(artifactPath)
		}
	}()
	if opts.SignKey != nil {
		buildprogress.Phase(ctx, "Signing binary", artifactPath)
		// pig additive (D18): sign before the record so its artifact digest
		// covers the signature block.
		signed, err := signature.Sign(artifactPath, signingManifest(lock, embedded), opts.SignKey)
		if err != nil {
			return "", "", fmt.Errorf("sign Piglet Binary: %w", err)
		}
		if !buildprogress.Enabled(ctx) {
			_, _ = fmt.Fprintf(stdout, "Piglet Binary signed by %s\n", signed.Signer.KeyID)
		}
	}
	buildprogress.Phase(ctx, "Checksumming and verifying binary", artifactPath)
	records, err := buildBinaryRecords(lock, p, artifactPath)
	if err != nil {
		return "", "", err
	}
	buildprogress.Phase(ctx, "Writing binary and records", "Publishing verified artifact to the managed store")
	recordPath, err := writeBinaryRecords(records, artifactPath)
	if err != nil {
		return "", "", err
	}
	cleanup = false
	if !buildprogress.Enabled(ctx) {
		_, _ = fmt.Fprintf(stdout, "Piglet Binary record: %s\n", recordPath)
	}
	return artifactPath, recordPath, nil
}

// signingManifest states what a Piglet Binary signature vouches for.
func signingManifest(lock buildLock, embedded []signature.EmbeddedFile) signature.Manifest {
	resolution := lock.ResolutionRecord.Resolution
	components := make([]signature.Component, 0, len(lock.ComponentPlan.Components))
	for _, component := range lock.ComponentPlan.Components {
		components = append(components, signature.Component{
			Kind: string(component.Kind), Name: component.Name, Realization: string(component.Realization),
			Materialization: string(component.Materialization), Digest: component.Origin.Digest,
		})
	}
	return signature.Manifest{
		Piglet: lock.Piglet, ReleaseVersion: lock.ReleaseVersion, Target: lock.Target, PigVersion: lock.PigVersion,
		PigletDigest: resolution.EffectiveDigest, SourceDigest: resolution.SourceDigest,
		ResolutionDigest: lock.ResolutionRecord.Digest, ComponentPlanDigest: lock.ComponentPlan.Digest,
		Components: components, Embedded: embedded,
	}
}

func buildBinaryRecords(lock buildLock, p *piglet.Piglet, artifactPath string) (binaryBuildRecords, error) {
	artifactDigest, size, err := hashFile(artifactPath)
	if err != nil {
		return binaryBuildRecords{}, fmt.Errorf("hash artifact: %w", err)
	}
	if err := smokeArtifact(artifactPath); err != nil {
		return binaryBuildRecords{}, err
	}
	// The managed copy is stored and later started under this name, so a
	// Windows artifact keeps the .exe its default output has.
	fileName := "pig-" + p.Name
	if p.Build != nil && p.Build.OutputName != "" {
		fileName = p.Build.OutputName
	} else if strings.HasPrefix(lock.Target, "windows/") {
		fileName += ".exe"
	}
	binaryRecord, err := pigletartifact.NewBinaryRecord(lock.Piglet, lock.ReleaseVersion, time.Now(), lock.ResolutionRecord, pigletartifact.BinaryInput{
		Target: lock.Target, PigVersion: lock.PigVersion,
		PigSourceRevision: lock.PigSourceRevision, PigSourceDigest: lock.PigSourceDigest,
		Builder: lock.Builder, BuilderIdentity: lock.BuilderIdentity, Toolchains: lock.Toolchains,
		Artifact: pigletartifact.Artifact{Digest: artifactDigest, Size: size, FileName: fileName},
		Verification: pigletartifact.Verification{
			Policy: "basic", Passed: true,
			Checks: []string{"artifact-sha256", "artifact-version-smoke", "extension-plan", "piglet-and-origins"},
		},
	})
	if err != nil {
		return binaryBuildRecords{}, fmt.Errorf("build Piglet Binary record: %w", err)
	}
	return binaryBuildRecords{Resolution: lock.ResolutionRecord, Binary: binaryRecord}, nil
}

func buildNativeLock(p *piglet.Piglet, cells []subprocess.CellSpec, opts Options) (buildLock, error) {
	lock, err := buildPortableLock(p, cells, opts)
	if err != nil {
		return buildLock{}, err
	}
	toolchains, err := toolchainVersions(cells)
	if err != nil {
		return buildLock{}, err
	}
	revision, sourceDigest, err := pigSourceIdentity()
	if err != nil {
		return buildLock{}, err
	}
	lock.PigVersion = coding.PigVersion
	lock.PigSourceRevision = revision
	lock.PigSourceDigest = sourceDigest
	lock.Builder = "native"
	lock.BuilderIdentity = "native:" + revision
	lock.Target = runtime.GOOS + "/" + runtime.GOARCH
	lock.Toolchains = toolchains
	return lock, nil
}

func buildPortableLock(p *piglet.Piglet, cells []subprocess.CellSpec, opts Options) (buildLock, error) {
	pigletPath := p.SourcePath()
	if pigletPath == "" {
		return buildLock{}, fmt.Errorf("Piglet source path is unavailable; build from a parsed Piglet file")
	}
	pigletDigest, _, err := hashFile(pigletPath)
	if err != nil {
		return buildLock{}, fmt.Errorf("hash Piglet source: %w", err)
	}
	if len(opts.BakedSettings) == 0 {
		return buildLock{}, fmt.Errorf("baked runtime Piglet is empty")
	}
	runtimeDigest := digestBytes(opts.BakedSettings)
	inputs, err := buildInputs(p, cells)
	if err != nil {
		return buildLock{}, err
	}
	deliveryPlan := BuildPlan(extensionInputsFromCells(cells), opts)
	componentPlan, err := buildPigletComponentPlan(cells, deliveryPlan, inputs)
	if err != nil {
		return buildLock{}, fmt.Errorf("build Piglet component plan: %w", err)
	}
	resolutionRecord, err := pigletartifact.NewResolutionRecord(p.Name, opts.Version, time.Now(), pigletartifact.ResolutionInput{
		SourceDigest: pigletDigest, EffectiveDigest: runtimeDigest,
		Inputs: resolutionInputs(inputs), ComponentPlan: componentPlan,
	})
	if err != nil {
		return buildLock{}, fmt.Errorf("build Piglet resolution record: %w", err)
	}
	return buildLock{
		Piglet: p.Name, ReleaseVersion: opts.Version,
		ComponentPlan: componentPlan, ResolutionRecord: resolutionRecord,
	}, nil
}

func buildInputs(p *piglet.Piglet, cells []subprocess.CellSpec) ([]buildInput, error) {
	lineage, _, _ := p.ResolutionIdentity()
	inputs := make([]buildInput, 0, max(0, len(lineage)-1))
	for i, entry := range lineage[:max(0, len(lineage)-1)] {
		inputs = append(inputs, buildInput{
			Kind: "piglet-base", Name: fmt.Sprintf("%02d-%s", i, filepath.Base(entry.Source)),
			Source: entry.Source, Version: entry.Version, Digest: entry.Digest,
		})
	}
	packages, err := piglet.ResolvePackages(p)
	if err != nil {
		return nil, err
	}
	for _, resolved := range packages {
		digest, err := packageManifestDigest(resolved.Root)
		if err != nil {
			return nil, fmt.Errorf("lock package %q: %w", resolved.Alias, err)
		}
		source, version, err := recordSource(resolved.Source, sourceref.BareReject, filepath.Dir(p.SourcePath()))
		if err != nil {
			return nil, fmt.Errorf("lock package %q source: %w", resolved.Alias, err)
		}
		inputs = append(inputs, buildInput{Kind: "package", Name: resolved.Alias, Source: source, Version: version, Digest: digest})
	}
	resolvedExtensions, extensionErrors := piglet.ResolveExtensions(p)
	if len(extensionErrors) > 0 {
		return nil, extensionErrors[0]
	}
	originByName := make(map[string]string, len(resolvedExtensions))
	for _, resolved := range resolvedExtensions {
		originByName[resolved.Entry.Name] = resolved.Origin
	}
	for _, cell := range cells {
		for _, config := range cell.Extensions {
			origin, ok := originByName[config.Name]
			if !ok {
				return nil, fmt.Errorf("lock extension %q: selected origin is unavailable", config.Name)
			}
			source, version, packageAlias, err := recordExtensionOrigin(origin, filepath.Dir(p.SourcePath()))
			if err != nil {
				return nil, fmt.Errorf("lock extension %q origin: %w", config.Name, err)
			}
			digest := strings.TrimSpace(config.ContentHash)
			if digest == "" {
				digest, err = hashTree(config.Source)
				if err != nil {
					return nil, fmt.Errorf("lock extension %q: %w", config.Name, err)
				}
			} else if !strings.HasPrefix(digest, "sha256:") {
				digest = "sha256:" + digest
			}
			inputs = append(inputs, buildInput{Kind: "extension", Name: config.Name, Source: source, Version: version, Package: packageAlias, Digest: digest})
			replacements, err := localGoReplacementDirs(config.Source)
			if err != nil {
				return nil, fmt.Errorf("lock extension %q local Go replacements: %w", config.Name, err)
			}
			for i, replacement := range replacements {
				replacementDigest, err := hashTree(replacement)
				if err != nil {
					return nil, fmt.Errorf("lock extension %q local Go replacement %s: %w", config.Name, replacement, err)
				}
				inputs = append(inputs, buildInput{
					Kind: "extension-dependency", Name: fmt.Sprintf("%s:go-replace:%d", config.Name, i),
					Source: "local:" + filepath.ToSlash(replacement), Digest: replacementDigest,
				})
			}
		}
	}
	resolvedSkills, skillErrors := piglet.ResolveSkills(p)
	if len(skillErrors) > 0 {
		return nil, skillErrors[0]
	}
	for _, skill := range resolvedSkills {
		digest, err := hashTree(skill.Path)
		if err != nil {
			return nil, fmt.Errorf("lock skill %q: %w", skill.Entry.Name, err)
		}
		source := "content-addressed"
		packageAlias := ""
		if alias, ok := strings.CutPrefix(skill.Origin, "package:"); ok {
			source = skill.Origin
			packageAlias = alias
		}
		inputs = append(inputs, buildInput{Kind: "skill", Name: skill.Entry.Name, Source: source, Package: packageAlias, Digest: digest})
	}
	slices.SortFunc(inputs, func(a, b buildInput) int {
		if value := strings.Compare(a.Kind, b.Kind); value != 0 {
			return value
		}
		return strings.Compare(a.Name, b.Name)
	})
	return inputs, nil
}

func resolutionInputs(inputs []buildInput) []pigletartifact.InputPin {
	pins := make([]pigletartifact.InputPin, 0, len(inputs))
	for _, input := range inputs {
		pins = append(pins, pigletartifact.InputPin{
			Kind: input.Kind, Name: input.Name, Source: input.Source,
			Version: input.Version, Digest: input.Digest, Package: input.Package,
		})
	}
	return pins
}

func recordExtensionOrigin(origin string, baseDir string) (string, string, string, error) {
	if alias, ok := strings.CutPrefix(origin, "package:"); ok {
		return origin, "", alias, nil
	}
	source, version, err := recordSource(origin, sourceref.BareReject, baseDir)
	return source, version, "", err
}

func recordSource(raw string, bare sourceref.BarePolicy, baseDir string) (string, string, error) {
	ref, err := sourceref.Parse(raw, sourceref.Options{Bare: bare, AllowContributed: true})
	if err != nil {
		return "", "", err
	}
	switch ref.Kind {
	case sourceref.KindNPM:
		identity, err := ref.Identity(baseDir)
		return identity, ref.NPMVer, err
	case sourceref.KindGit:
		identity, err := ref.Identity(baseDir)
		return identity, ref.GitRef, err
	case sourceref.KindLocal:
		identity, err := ref.Identity(baseDir)
		return identity, "", err
	case sourceref.KindContributed:
		return ref.Scheme + ":sha256:" + hex.EncodeToString(sha256Sum([]byte(ref.Locator))), "", nil
	default:
		return "", "", fmt.Errorf("unsupported source kind %q", ref.Kind)
	}
}

func packageManifestDigest(root string) (string, error) {
	paths := []string{"package.json", "plugin.json", ".plugin/plugin.json", ".claude-plugin/plugin.json", ".cursor-plugin/plugin.json", ".pig-plugin/plugin.json"}
	hash := sha256.New()
	found := false
	for _, relative := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		found = true
		_, _ = hash.Write([]byte(relative))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{0})
	}
	if !found {
		return "", fmt.Errorf("no Package or Plugin manifest found")
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func localGoReplacementDirs(root string) ([]string, error) {
	if root == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	parsed, err := modfile.Parse(filepath.Join(root, "go.mod"), data, nil)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var paths []string
	for _, replacement := range parsed.Replace {
		target := replacement.New.Path
		if target == "" || replacement.New.Version != "" || (!filepath.IsAbs(target) && !strings.HasPrefix(target, ".")) {
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(root, target)
		}
		target, err = filepath.Abs(target)
		if err != nil {
			return nil, err
		}
		target, err = filepath.EvalSymlinks(target)
		if err != nil {
			return nil, fmt.Errorf("resolve local Go replacement %s: %w", replacement.New.Path, err)
		}
		if _, exists := seen[target]; exists {
			continue
		}
		seen[target] = struct{}{}
		paths = append(paths, target)
	}
	slices.Sort(paths)
	return paths, nil
}

func hashTree(root string) (string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(root)
		if err != nil {
			return "", err
		}
		hash := sha256.New()
		_, _ = hash.Write([]byte(filepath.Base(root)))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(info.Mode().String()))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
		return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
	}
	hash := sha256.New()
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				return nil
			}
			return fmt.Errorf("symlink %s is not supported in build locks", path)
		}
		if entry.IsDir() {
			name := entry.Name()
			if path != root && (name == ".git" || name == "node_modules" || name == "target" || name == "dist" || name == "build" || name == "__pycache__" || name == ".venv" || name == "venv") {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		_, _ = hash.Write([]byte(filepath.ToSlash(relative)))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(info.Mode().String()))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func hashFile(path string) (string, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	return digestBytes(data), int64(len(data)), nil
}

func digestBytes(data []byte) string {
	return "sha256:" + hex.EncodeToString(sha256Sum(data))
}

func sha256Sum(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func smokeArtifact(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("basic artifact verification failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if strings.TrimSpace(string(output)) == "" {
		return fmt.Errorf("basic artifact verification failed: --version returned empty output")
	}
	return nil
}

func pigSourceIdentity() (string, string, error) {
	root, err := pigSourceRoot()
	if err != nil {
		return "", "", err
	}
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return "", "", fmt.Errorf("resolve Pig source revision: %w", err)
	}
	revision := strings.TrimSpace(string(output))
	if revision == "" {
		return "", "", fmt.Errorf("resolve Pig source revision: git returned an empty revision")
	}
	command = exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	command.Dir = root
	output, err = command.Output()
	if err != nil {
		return "", "", fmt.Errorf("enumerate Pig build sources: %w", err)
	}
	paths := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	slices.Sort(paths)
	hash := sha256.New()
	for _, relative := range paths {
		if relative == "" {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(relative))
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", "", fmt.Errorf("lock Pig source %s: %w", relative, err)
		}
		if info.IsDir() {
			continue
		}
		var data []byte
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return "", "", fmt.Errorf("read Pig source symlink %s: %w", relative, err)
			}
			data = []byte(target)
		} else {
			data, err = os.ReadFile(path)
			if err != nil {
				return "", "", fmt.Errorf("read Pig source %s: %w", relative, err)
			}
		}
		_, _ = hash.Write([]byte(filepath.ToSlash(relative)))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(info.Mode().String()))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{0})
	}
	return revision, "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func toolchainVersions(cells []subprocess.CellSpec) (map[string]string, error) {
	commands := map[string][]string{"go": {"go", "version"}}
	for _, cell := range cells {
		switch cell.Language {
		case "rust":
			commands["rust"] = []string{"cargo", "--version"}
		case "python":
			commands["python"] = []string{"python3", "--version"}
		case "node":
			commands["node"] = []string{"node", "--version"}
		}
	}
	versions := make(map[string]string, len(commands))
	for name, command := range commands {
		output, err := exec.Command(command[0], command[1:]...).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("resolve %s toolchain identity: %w: %s", name, err, strings.TrimSpace(string(output)))
		}
		version := strings.TrimSpace(string(output))
		if version == "" {
			return nil, fmt.Errorf("resolve %s toolchain identity: command returned empty output", name)
		}
		versions[name] = version
	}
	return versions, nil
}

func writeBinaryRecords(records binaryBuildRecords, artifactPath string) (string, error) {
	if err := pigletartifact.ValidateBinaryLink(records.Resolution, records.Binary); err != nil {
		return "", err
	}
	resolution := records.Resolution.Resolution
	binary := records.Binary.Binary
	if resolution == nil || binary == nil {
		return "", fmt.Errorf("Piglet Binary records have invalid kinds")
	}
	artifactDigest, artifactSize, err := hashFile(artifactPath)
	if err != nil {
		return "", err
	}
	if artifactDigest != binary.Artifact.Digest || artifactSize != binary.Artifact.Size {
		return "", fmt.Errorf("Piglet Binary artifact does not match its record")
	}
	version := records.Binary.ReleaseVersion
	if version == "" {
		version = "unversioned"
	}
	root := filepath.Join(
		codingagent.PigletRecordsDir(), records.Binary.Piglet,
		strings.TrimPrefix(resolution.SourceDigest, "sha256:"), version,
	)
	resolutionPath := filepath.Join(root, string(pigletartifact.RecordKindResolution), strings.TrimPrefix(records.Resolution.Digest, "sha256:")+".json")
	binaryPath := filepath.Join(
		root, string(pigletartifact.RecordKindBinary), strings.ReplaceAll(binary.Target, "/", "-"),
		strings.TrimPrefix(records.Binary.Digest, "sha256:")+".json",
	)
	managedArtifact := filepath.Join(
		codingagent.PigletArtifactsDir(), records.Binary.Piglet,
		strings.TrimPrefix(resolution.SourceDigest, "sha256:"), version,
		strings.ReplaceAll(binary.Target, "/", "-"),
		strings.TrimPrefix(binary.Artifact.Digest, "sha256:"), binary.Artifact.FileName,
	)
	artifactCreated, err := writeManagedArtifact(artifactPath, managedArtifact, binary.Artifact.Digest)
	if err != nil {
		return "", err
	}
	resolutionCreated, err := writeRecordFile(resolutionPath, records.Resolution)
	if err != nil {
		if artifactCreated {
			_ = os.Remove(managedArtifact)
		}
		return "", err
	}
	if _, err := writeRecordFile(binaryPath, records.Binary); err != nil {
		if resolutionCreated {
			_ = os.Remove(resolutionPath)
		}
		if artifactCreated {
			_ = os.Remove(managedArtifact)
		}
		return "", err
	}
	return binaryPath, nil
}

func writeRecordFile(path string, record pigletartifact.Record) (bool, error) {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return false, err
	}
	data = append(data, '\n')
	if existing, err := os.ReadFile(path); err == nil {
		if slices.Equal(existing, data) {
			return false, nil
		}
		return false, fmt.Errorf("Piglet record %s already exists with different content", path)
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	stage, err := os.CreateTemp(filepath.Dir(path), ".record-*.stage")
	if err != nil {
		return false, err
	}
	stagePath := stage.Name()
	defer func() { _ = os.Remove(stagePath) }()
	if _, err := stage.Write(data); err != nil {
		_ = stage.Close()
		return false, err
	}
	if err := stage.Chmod(0o644); err != nil {
		_ = stage.Close()
		return false, err
	}
	if err := stage.Close(); err != nil {
		return false, err
	}
	if err := os.Link(stagePath, path); err != nil {
		return false, fmt.Errorf("commit Piglet record %s: %w", path, err)
	}
	return true, nil
}

func writeManagedArtifact(source, target, expectedDigest string) (bool, error) {
	if _, err := os.Stat(target); err == nil {
		digest, _, err := hashFile(target)
		if err != nil {
			return false, err
		}
		if digest != expectedDigest {
			return false, fmt.Errorf("managed artifact %s exists with digest %s, expected %s", target, digest, expectedDigest)
		}
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return false, err
	}
	input, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer func() { _ = input.Close() }()
	stage, err := os.CreateTemp(filepath.Dir(target), ".artifact-*.stage")
	if err != nil {
		return false, err
	}
	stagePath := stage.Name()
	defer func() { _ = os.Remove(stagePath) }()
	if _, err := io.Copy(stage, input); err != nil {
		_ = stage.Close()
		return false, err
	}
	if err := stage.Chmod(0o755); err != nil {
		_ = stage.Close()
		return false, err
	}
	if err := stage.Close(); err != nil {
		return false, err
	}
	digest, _, err := hashFile(stagePath)
	if err != nil {
		return false, err
	}
	if digest != expectedDigest {
		return false, fmt.Errorf("staged artifact digest %s does not match expected %s", digest, expectedDigest)
	}
	if err := os.Link(stagePath, target); err != nil {
		return false, fmt.Errorf("commit managed artifact %s: %w", target, err)
	}
	return true, nil
}
