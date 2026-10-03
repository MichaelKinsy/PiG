package pigletbuild

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/buildprogress"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/linkerexec"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// pigModulePath is the Go module whose source a native Piglet Binary build compiles.
const pigModulePath = "github.com/MichaelKinsy/PiG"

// sourceUnavailableRemedy is the remedy for a native build with no Pig source to compile.
const sourceUnavailableRemedy = "run from a Pig checkout or set PIG_SOURCE_ROOT"

// releaseSourceVersion names the tagged module version a release binary was built from. A release
// archive is built from extracted source without VCS metadata, so the release build stamps it with
// -ldflags "-X github.com/MichaelKinsy/PiG/coding/pigletbuild.releaseSourceVersion=v<version>".
var releaseSourceVersion string

// pigSource is the Pig source tree one native build compiles.
type pigSource struct {
	Root string
	// ModuleVersion is set when Root is the staged copy of the running release's
	// checksum-verified Go module cache tree. That tree has no VCS metadata, and its go.work
	// names modules the module zip omits, so it builds with GOWORK=off.
	ModuleVersion string
	// Revision is the source commit the module proxy reports for ModuleVersion, if any.
	Revision string
}

// modDownloadResult is the subset of `go mod download -json` output the native builder reads.
type modDownloadResult struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
	Dir     string `json:"Dir"`
	Sum     string `json:"Sum"`
	Error   string `json:"Error"`
	Origin  *struct {
		Hash string `json:"Hash"`
	} `json:"Origin"`
}

// runningPigRelease reports the tagged module version of the running binary when its source is
// exactly that release: a `go install …@v<version>` binary, or a release archive binary stamped
// with releaseSourceVersion. A development build reports false.
func runningPigRelease() (string, bool) {
	want := "v" + coding.PigVersion
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Path == pigModulePath && info.Main.Version == want {
		return want, true
	}
	if strings.TrimSpace(releaseSourceVersion) == want {
		return want, true
	}
	return "", false
}

// releaseFor returns the fetchable release version of the running binary.
func (b nativeBuilder) releaseFor() (string, bool) {
	if b.runningRelease != nil {
		return b.runningRelease()
	}
	return runningPigRelease()
}

// resolveSource returns the local Pig checkout or, for a release binary without one, fetches
// exactly the running release through the Go module proxy. GOPROXY and GOSUMDB verify the
// download, and GOMODCACHE keeps it for later builds.
func (b nativeBuilder) resolveSource(ctx context.Context, stderr io.Writer) (pigSource, error) {
	root, localErr := pigSourceRoot()
	if localErr == nil {
		return pigSource{Root: root}, nil
	}
	version, ok := b.releaseFor()
	if !ok {
		return pigSource{}, fmt.Errorf("source-unavailable: %w; this PiG build is not a tagged release whose source can be fetched; remedy: %s", localErr, sourceUnavailableRemedy)
	}
	buildprogress.Phase(ctx, "Fetching PiG source", pigModulePath+"@"+version)
	if stderr != nil {
		_, _ = fmt.Fprintf(stderr, "fetching PiG %s source (cached after first build)\n", version)
	}
	download := b.modDownload
	if download == nil {
		download = goModDownload
	}
	output, err := download(ctx, pigModulePath+"@"+version)
	var result modDownloadResult
	decodeErr := json.Unmarshal(output, &result)
	if err != nil || decodeErr != nil || result.Error != "" {
		detail := result.Error
		switch {
		case detail != "":
		case err != nil:
			detail = err.Error()
		default:
			detail = "decode go mod download output: " + decodeErr.Error()
		}
		return pigSource{}, fmt.Errorf("source-unavailable: fetch PiG %s source through the Go module proxy: %s; remedy: %s", version, strings.TrimSpace(detail), sourceUnavailableRemedy)
	}
	if result.Path != pigModulePath || result.Version != version || result.Dir == "" || !isPigModule(result.Dir) {
		return pigSource{}, fmt.Errorf("source-unavailable: go mod download returned %s@%s at %q, not the PiG %s source; remedy: %s", result.Path, result.Version, result.Dir, version, sourceUnavailableRemedy)
	}
	root, err = materializeModuleSource(result.Dir, version, result.Sum)
	if err != nil {
		return pigSource{}, fmt.Errorf("source-unavailable: stage PiG %s source: %w; remedy: %s", version, err, sourceUnavailableRemedy)
	}
	source := pigSource{Root: root, ModuleVersion: version}
	if result.Origin != nil {
		source.Revision = strings.TrimSpace(result.Origin.Hash)
	}
	return source, nil
}

// pigSourceCacheDir is the Pig cache directory holding staged release source trees.
func pigSourceCacheDir() string {
	return filepath.Join(codingagent.ConfigRoot(), "cache", "pig-source")
}

// materializeModuleSource stages the module cache tree outside GOMODCACHE, because the Go command
// refuses a build overlay for any file beneath GOMODCACHE. The stage is keyed by version and
// module sum, published by one rename, and reused by later builds. Files are hard links to the
// module cache when the filesystem allows and copies otherwise; the build never writes them.
// Concurrent first builds each stage a tree. The first rename publishes; the others find its
// marker and reuse it. A published tree is never removed, because a concurrent build may be
// compiling from it. Only a target without the marker, which no publish leaves, is replaced.
func materializeModuleSource(moduleDir, version, sum string) (string, error) {
	if sum == "" {
		return "", fmt.Errorf("go mod download reported no module sum")
	}
	key := sha256.Sum256([]byte(sum))
	cacheDir := pigSourceCacheDir()
	target := filepath.Join(cacheDir, version+"-"+hex.EncodeToString(key[:8]))
	marker := filepath.Join(target, ".pig-source-sum")
	published := func() bool {
		data, err := os.ReadFile(marker)
		return err == nil && string(data) == sum
	}
	if published() {
		return target, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(cacheDir, ".stage-"+version+"-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := linkOrCopyTree(moduleDir, stage); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(stage, ".pig-source-sum"), []byte(sum), 0o644); err != nil {
		return "", err
	}
	err = os.Rename(stage, target)
	if err == nil || published() {
		return target, nil
	}
	if _, statErr := os.Lstat(target); statErr != nil {
		return "", err
	}
	if err := os.RemoveAll(target); err != nil && !published() {
		return "", err
	}
	if err := os.Rename(stage, target); err != nil && !published() {
		return "", err
	}
	return target, nil
}

// linkOrCopyTree reproduces the regular files and directories of src under dst.
func linkOrCopyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("module source %s is not a regular file", relative)
		}
		if os.Link(path, target) == nil {
			return nil
		}
		return copyRegularFile(path, target)
	})
}

func copyRegularFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	input, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm()|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}

// goModDownload runs `go mod download -json` outside any workspace and returns its stdout, which
// carries a JSON Error field when the download fails.
func goModDownload(ctx context.Context, query string) ([]byte, error) {
	goToolchain, err := toolchain.ResolveGo()
	if err != nil {
		return nil, err
	}
	command := linkerexec.CommandContext(ctx, goToolchain.Command, "mod", "download", "-json", query)
	command.Dir = os.TempDir()
	command.Env = goToolchain.Environ(moduleSourceBuildEnv(os.Environ()))
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("go mod download %s: %s", query, message)
	}
	return stdout.Bytes(), nil
}

// moduleSourceBuildEnv runs the Go command against a read-only module cache tree: no workspace,
// and no -mod flag that would write go.mod or select an absent vendor directory.
func moduleSourceBuildEnv(env []string) []string {
	out := make([]string, 0, len(env)+2)
	goflags := ""
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "GOWORK="):
		case strings.HasPrefix(kv, "GOFLAGS="):
			var kept []string
			for f := range strings.FieldsSeq(strings.TrimPrefix(kv, "GOFLAGS=")) {
				if !strings.HasPrefix(f, "-mod=") {
					kept = append(kept, f)
				}
			}
			goflags = strings.Join(kept, " ")
		default:
			out = append(out, kv)
		}
	}
	return append(out, "GOWORK=off", "GOFLAGS="+goflags)
}

// buildEnv is the Go command environment for compiling this source tree.
func (s pigSource) buildEnv(env []string) []string {
	if s.ModuleVersion != "" {
		return moduleSourceBuildEnv(env)
	}
	return sourceBuildEnv(s.Root, env)
}

// sourceFiles lists the build source files of the tree relative to Root: Git's tracked and
// untracked non-ignored files for a checkout, and every regular file or symlink for a module
// cache copy.
func (s pigSource) sourceFiles() ([]string, error) {
	if s.ModuleVersion == "" {
		command := linkerexec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard", "-z")
		command.Dir = s.Root
		output, err := command.Output()
		if err != nil {
			return nil, fmt.Errorf("enumerate Pig build sources: %w", err)
		}
		return strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00"), nil
	}
	var paths []string
	err := filepath.WalkDir(s.Root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(s.Root, path)
		if err != nil {
			return err
		}
		if relative == ".pig-source-sum" {
			return nil
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("enumerate Pig module sources: %w", err)
	}
	return paths, nil
}

// revision identifies the source: the checkout's HEAD commit, or the proxy-reported commit of a
// fetched release, falling back to its module version.
func (s pigSource) revision() (string, error) {
	if s.ModuleVersion != "" {
		if s.Revision != "" {
			return s.Revision, nil
		}
		return s.ModuleVersion, nil
	}
	command := linkerexec.Command("git", "rev-parse", "HEAD")
	command.Dir = s.Root
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("resolve Pig source revision: %w", err)
	}
	revision := strings.TrimSpace(string(output))
	if revision == "" {
		return "", fmt.Errorf("resolve Pig source revision: git returned an empty revision")
	}
	return revision, nil
}
