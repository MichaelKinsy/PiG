package pigletbuild

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/cellpack"
	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	pigletartifact "github.com/MichaelKinsy/PiG/coding/piglet/artifact"
	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
	"github.com/MichaelKinsy/PiG/internal/buildprogress"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// buildNativeArtifact builds a Piglet Binary from already-resolved cells and the immutable
// executable component plan. It overlays binary-materialized subprocess cells and
// fused factories onto the Pig source and rebuilds Pig without writing the tree. Cells the plan
// does not materialize in the binary are reported so toolchain-dependence is
// never hidden. The caller validates resolution first.
func buildNativeArtifact(ctx context.Context, p *piglet.Piglet, cells []subprocess.CellSpec, componentPlan pigletartifact.Plan, resolution *pigletartifact.Record, opts Options, outPath string, stdout, stderr io.Writer) ([]signature.EmbeddedFile, error) {
	sourceRoot, err := pigSourceRoot()
	if err != nil {
		return nil, err
	}
	cacheRoot, err := os.MkdirTemp("", "piglet-binary-build-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(cacheRoot) }()

	host := Target{OS: runtime.GOOS, Arch: runtime.GOARCH}
	var built []stagedCell
	var fused []fusedEntry
	for _, cell := range cells {
		realization, _, err := cellComponentDisposition(cell, componentPlan)
		if err != nil {
			return nil, err
		}
		names := make([]string, len(cell.Extensions))
		for i, ext := range cell.Extensions {
			names[i] = ext.Name
		}
		members := strings.Join(names, ", ")
		cellCtx := buildprogress.Member(ctx, members)
		if realization == pigletartifact.RealizationFused {
			buildprogress.Phase(ctx, "Preparing fused Go members", members)
			if !isGoFactoryCell(cell) {
				return nil, fmt.Errorf("component plan: cell %q is fused but is not a Go factory cell", cell.Key)
			}
			entries, err := collectFused(cell)
			if err != nil {
				return nil, err
			}
			fused = append(fused, entries...)
			continue
		}
		buildprogress.Phase(ctx, "Building "+cell.Language+" members", members)
		var sc stagedCell
		switch cell.Strategy {
		case subprocess.CellStrategyPackedGo:
			sc, err = buildGoCell(cellCtx, cell, cacheRoot, host)
		case subprocess.CellStrategyPackedRust:
			sc, err = buildRustCell(cellCtx, cell, cacheRoot, host)
		case subprocess.CellStrategyIsolated:
			cfg := cell.Extensions[0]
			switch {
			case cfg.EntrypointKind != "factory":
				sc, err = buildSourceCell(cellCtx, cfg, cacheRoot, host)
			case cfg.RuntimeLanguage == "go":
				sc, err = buildGoCell(cellCtx, cell, cacheRoot, host)
			case cfg.RuntimeLanguage == "rust":
				sc, err = buildRustCell(cellCtx, cell, cacheRoot, host)
			default:
				_, _ = fmt.Fprintf(stderr, "warn: [not-embedded] isolated %s factory cell %q is not embedded yet; the Piglet Binary will build it at runtime and needs a toolchain\n", cfg.RuntimeLanguage, cell.Key)
				continue
			}
		default:
			_, _ = fmt.Fprintf(stderr, "warn: [not-embedded] cell %q (%s) is not embedded; the Piglet Binary will build it at runtime and needs a toolchain\n", cell.Key, cell.Strategy)
			continue
		}
		if err != nil {
			return nil, err
		}
		built = append(built, sc)
	}
	if len(built) == 0 && len(fused) == 0 {
		return nil, fmt.Errorf("piglet %q has nothing to embed or fuse; nothing to build", p.Name)
	}
	// pig additive (D31): linked extensions must not invoke process-global
	// operations that are isolated by the normal subprocess realization.
	if len(fused) > 0 {
		buildprogress.Phase(ctx, "Checking fused Go members", "Process-global safety checks")
	}
	if err := vetFusedPackages(ctx, fused); err != nil {
		return nil, err
	}

	buildprogress.Phase(ctx, "Packing build inputs", "Embedding cells, resources, and component records")
	overlayPath, err := writeBuildOverlay(filepath.Join(cacheRoot, "overlay"), sourceRoot, built, fused, resolution, opts)
	if err != nil {
		return nil, err
	}

	if outPath == "" {
		outPath = filepath.Join(".", "pig-"+p.Name)
	}
	abary, err := filepath.Abs(outPath)
	if err != nil {
		return nil, err
	}
	buildprogress.Phase(ctx, "Compiling and linking Go binary", p.Name+" → "+abary)
	if !buildprogress.Enabled(ctx) {
		_, _ = fmt.Fprintf(stdout, "building Piglet Binary %s (%d embedded cell(s), %d fused, target %s)...\n", p.Name, len(built), len(fused), host)
	}
	buildArgs := buildprogress.ToolArgs(ctx, "go", pigletBinaryBuildArgs(abary, opts.Version, overlayPath))
	goCommand, err := toolchain.Go()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, goCommand, buildArgs...)
	cmd.Dir = sourceRoot
	cmd.Env = sourceBuildEnv(sourceRoot, os.Environ())
	if err := buildprogress.Run(buildprogress.Member(ctx, p.Name), cmd); err != nil {
		return nil, fmt.Errorf("build Piglet Binary: %w", err)
	}
	if !buildprogress.Enabled(ctx) {
		_, _ = fmt.Fprintf(stdout, "Piglet Binary written: %s\n", abary)
	}
	if len(built) > 0 {
		buildprogress.Phase(ctx, "Checksumming embedded cells", p.Name)
	}
	return embeddedFiles(built)
}

// writeBuildOverlay routes the Piglet-specific embed inputs, fuse registry,
// go.mod, and signer key to the compiler through one `go build -overlay`, so a
// build never writes into the Pig source tree and concurrent builds never
// observe each other. It returns the overlay description path.
func writeBuildOverlay(dir, sourceRoot string, built []stagedCell, fused []fusedEntry, resolution *pigletartifact.Record, opts Options) (string, error) {
	overlay, err := newBuildOverlay(dir)
	if err != nil {
		return "", err
	}
	if len(built) > 0 {
		manifest := cellpack.Manifest{
			PigCoreVersion: coding.PigVersion,
			Cells:          make([]cellpack.CellEntry, len(built)),
		}
		for i, sc := range built {
			manifest.Cells[i] = sc.entry
		}
		if err := overlayCells(overlay, sourceRoot, manifest, built); err != nil {
			return "", err
		}
	}
	if len(fused) > 0 {
		if err := overlayFuse(overlay, sourceRoot, fused); err != nil {
			return "", err
		}
	}
	if len(opts.BakedSettings) > 0 {
		if err := overlay.bytes(filepath.Join(sourceRoot, bakedPigletDir, "piglet.yaml"), opts.BakedSettings); err != nil {
			return "", err
		}
	}
	if opts.SignKey != nil {
		if err := overlaySigner(overlay, sourceRoot, opts.SignKey); err != nil {
			return "", err
		}
	}
	if resolution != nil {
		// pig additive (D18): bake the Piglet resolution record so the built
		// binary can verify its own build identity at startup.
		closureJSON, err := json.Marshal(resolution)
		if err != nil {
			return "", fmt.Errorf("marshal Piglet closure: %w", err)
		}
		if err := overlay.bytes(filepath.Join(sourceRoot, bakedPigletDir, "resolution-record.json"), closureJSON); err != nil {
			return "", err
		}
	}
	return overlay.write()
}

// pigletBinaryBuildArgs builds the `go build` argv, applies the build overlay,
// and bakes the Piglet release version when present. Binaries are always stripped and trimmed: -s -w
// drop the symbol table and DWARF (Go stack traces come from pclntab, so panics
// stay legible and only debugger attachment is given up), and -trimpath keeps
// absolute build paths out of a redistributable artifact.
func pigletBinaryBuildArgs(outPath, version, overlayPath string) []string {
	ld := []string{"-s", "-w"}
	if v := strings.TrimSpace(version); v != "" {
		ld = append(ld, "-X main.PigletBinaryVersion="+v)
	}
	args := []string{"build", "-buildvcs=false", "-trimpath", "-ldflags", strings.Join(ld, " ")}
	if overlayPath != "" {
		args = append(args, "-overlay", overlayPath)
	}
	return append(args, "-o", outPath, "./cmd/pig")
}

type stagedCell struct {
	entry      cellpack.CellEntry
	binaryPath string // source path of the built binary to copy into the tree
}

func buildGoCell(ctx context.Context, cell subprocess.CellSpec, cacheRoot string, host Target) (stagedCell, error) {
	goExts, err := goExtensions(cell)
	if err != nil {
		return stagedCell{}, err
	}
	packed, err := runtimecell.BuildGoPackedCell(ctx, cacheRoot, cell.Key, goExts)
	if err != nil {
		return stagedCell{}, fmt.Errorf("build cell %q: %w", cell.Key, err)
	}
	exts := make([]cellpack.ExtEntry, len(goExts))
	for i, e := range goExts {
		exts[i] = cellpack.ExtEntry{Name: e.Name, Hash: e.Hash}
	}
	rel := "go/" + shortHash(packed.Hash) + "/runner"
	return stagedCell{
		entry: cellpack.CellEntry{
			Language: "go", Key: cell.Key, Strategy: string(cell.Strategy), OS: host.OS, Arch: host.Arch,
			Binary: rel, Extensions: exts,
		},
		binaryPath: packed.BinaryPath,
	}, nil
}

func buildRustCell(ctx context.Context, cell subprocess.CellSpec, cacheRoot string, host Target) (stagedCell, error) {
	rustExts, err := rustExtensions(cell)
	if err != nil {
		return stagedCell{}, err
	}
	packed, err := runtimecell.BuildRustPackedCell(ctx, cacheRoot, cell.Key, rustExts)
	if err != nil {
		return stagedCell{}, fmt.Errorf("build cell %q: %w", cell.Key, err)
	}
	exts := make([]cellpack.ExtEntry, len(rustExts))
	for i, e := range rustExts {
		exts[i] = cellpack.ExtEntry{Name: e.Name, Hash: e.Hash}
	}
	rel := "rust/" + shortHash(packed.Hash) + "/runner"
	return stagedCell{
		entry: cellpack.CellEntry{
			Language: "rust", Key: cell.Key, Strategy: string(cell.Strategy), OS: host.OS, Arch: host.Arch,
			Binary: rel, Extensions: exts,
		},
		binaryPath: packed.BinaryPath,
	}, nil
}

// goExtensions returns the packed-go descriptors for a cell. It covers a shared
// packed-go cell and a single fissioned Go factory cell,
// which the runtime builds identically via BuildGoPackedCell keyed by cell.Key.
// The single-cell mapping mirrors core's goExtensionFromConfig.
func goExtensions(cell subprocess.CellSpec) ([]runtimecell.GoExtension, error) {
	if cell.Strategy == subprocess.CellStrategyPackedGo {
		return cell.GoExtensions()
	}
	cfg := cell.Extensions[0]
	if cfg.Source == "" || cfg.Package == "" || cfg.Factory != "Extension" {
		return nil, fmt.Errorf("go factory %q must provide source/package and func Extension() *sdk.Extension", cfg.Name)
	}
	return []runtimecell.GoExtension{{
		Name: cfg.Name, Root: cfg.Source, ModulePath: cfg.ModulePath,
		Package: cfg.Package, Factory: cfg.Factory, Hash: extHash(cfg), WorkspaceModules: append([]string(nil), cfg.GoWorkspaceModules...),
	}}, nil
}

// rustExtensions mirrors goExtensions for packed-rust and isolated rust-factory
// cells. The single-cell mapping mirrors core's rustExtensionFromConfig.
func rustExtensions(cell subprocess.CellSpec) ([]runtimecell.RustExtension, error) {
	if cell.Strategy == subprocess.CellStrategyPackedRust {
		return cell.RustExtensions()
	}
	cfg := cell.Extensions[0]
	if cfg.Source == "" || cfg.Package == "" || cfg.Factory != "new_extension" {
		return nil, fmt.Errorf("rust factory %q must provide source/package and pub fn new_extension() -> Extension", cfg.Name)
	}
	return []runtimecell.RustExtension{{
		Name: cfg.Name, Root: cfg.Source, Package: cfg.Package,
		Factory: cfg.Factory, Hash: extHash(cfg),
	}}, nil
}

// extHash mirrors core's valueOr(cfg.ContentHash, cfg.Source): the per-extension
// identity the runtime recomputes and the resolver matches on.
func extHash(cfg subprocess.ExtConfig) string {
	if cfg.ContentHash != "" {
		return cfg.ContentHash
	}
	return cfg.Source
}

func shortHash(h string) string {
	if len(h) >= 16 {
		return h[:16]
	}
	if h == "" {
		return "unknown"
	}
	return h
}

// buildSourceCell compiles an isolated standalone source extension via the same builder the runtime uses, so the recorded
// language and source hash match what the Piglet Binary will request at startup.
func buildSourceCell(ctx context.Context, cfg subprocess.ExtConfig, cacheRoot string, host Target) (stagedCell, error) {
	if cfg.Source == "" {
		return stagedCell{}, fmt.Errorf("isolated extension %q has no source dir to build", cfg.Name)
	}
	b := subprocess.NewBuilder(filepath.Join(cacheRoot, "src"))
	res, err := b.BuildContext(ctx, cfg.Name, cfg.Source)
	if err != nil {
		return stagedCell{}, fmt.Errorf("build source cell %q: %w", cfg.Name, err)
	}
	rel := "isolated/" + res.Language + "/" + cfg.Name + "-" + shortHash(res.Hash)
	return stagedCell{
		entry: cellpack.CellEntry{
			Language: res.Language, Key: cfg.Name, Strategy: string(subprocess.CellStrategyIsolated), OS: host.OS, Arch: host.Arch,
			Binary: rel, Extensions: []cellpack.ExtEntry{{Name: cfg.Name, Hash: res.Hash}},
		},
		binaryPath: res.BinaryPath,
	}, nil
}

// bakedPigletDir is the source-relative package whose embedded files carry a
// Piglet Binary's baked Piglet and resolution record.
var bakedPigletDir = filepath.Join("coding", "pigletbuild", "binarypiglet")

// buildOverlay is one `go build -overlay` replacement set. Generated contents
// are written under dir; the Pig source tree itself is never modified.
type buildOverlay struct {
	dir     string
	replace map[string]string
}

func newBuildOverlay(dir string) (*buildOverlay, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &buildOverlay{dir: dir, replace: map[string]string{}}, nil
}

// file makes target read as the existing file backing.
func (o *buildOverlay) file(target, backing string) {
	o.replace[target] = backing
}

// bytes makes target read as data.
func (o *buildOverlay) bytes(target string, data []byte) error {
	backing := filepath.Join(o.dir, fmt.Sprintf("%03d-%s", len(o.replace), filepath.Base(target)))
	if err := os.WriteFile(backing, data, 0o644); err != nil {
		return fmt.Errorf("write build overlay for %s: %w", target, err)
	}
	o.file(target, backing)
	return nil
}

// write stores the overlay description and returns its path for -overlay.
func (o *buildOverlay) write() (string, error) {
	data, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{o.replace})
	if err != nil {
		return "", err
	}
	path := filepath.Join(o.dir, "overlay.json")
	return path, os.WriteFile(path, data, 0o644)
}

// overlaySigner embeds the signing key's public half so the built binary
// refuses to start without a valid signature by it.
func overlaySigner(o *buildOverlay, sourceRoot string, key ed25519.PrivateKey) error {
	public, err := signature.MarshalPublicKey(key.Public().(ed25519.PublicKey))
	if err != nil {
		return err
	}
	return o.bytes(filepath.Join(sourceRoot, bakedPigletDir, "signers.txt"), public)
}

// embeddedFiles digests each cell binary compiled into the Piglet Binary for
// its signed manifest.
func embeddedFiles(built []stagedCell) ([]signature.EmbeddedFile, error) {
	files := make([]signature.EmbeddedFile, 0, len(built))
	for _, sc := range built {
		digest, _, err := hashFile(sc.binaryPath)
		if err != nil {
			return nil, fmt.Errorf("digest embedded cell %s: %w", sc.entry.Binary, err)
		}
		files = append(files, signature.EmbeddedFile{Path: "cells/" + sc.entry.Binary, Digest: digest})
	}
	return files, nil
}

// overlayCells adds the embedded cell manifest and cell binaries to the
// cellpack embed directory of the build.
func overlayCells(o *buildOverlay, sourceRoot string, manifest cellpack.Manifest, built []stagedCell) error {
	cellsDir := filepath.Join(sourceRoot, "coding", "extension", "host", "cellpack", "cells")
	for _, sc := range built {
		o.file(filepath.Join(cellsDir, filepath.FromSlash(sc.entry.Binary)), sc.binaryPath)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return o.bytes(filepath.Join(cellsDir, "manifest.json"), append(data, '\n'))
}

// pigSourceRoot locates the github.com/MichaelKinsy/PiG checkout used by the native
// Piglet Binary builder.
func pigSourceRoot() (string, error) {
	if r := strings.TrimSpace(os.Getenv("PIG_SOURCE_ROOT")); r != "" {
		return r, nil
	}
	if wd, err := os.Getwd(); err == nil {
		for dir := wd; ; {
			if isPigModule(dir) {
				return dir, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", fmt.Errorf("cannot locate the github.com/MichaelKinsy/PiG source checkout; set PIG_SOURCE_ROOT")
}

func isPigModule(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	return err == nil && strings.Contains(string(data), "module github.com/MichaelKinsy/PiG")
}

// fusedEntry is one compatible Go extension linked into the Piglet Binary.
type fusedEntry struct {
	Name             string
	Pkg              string
	Factory          string
	Root             string
	ModulePath       string
	Package          string
	WorkspaceModules []string
}

// isGoFactoryCell reports whether a planned cell is a Go SDK-factory cell, the
// only kind the Piglet Binary can fuse in-process today.
func isGoFactoryCell(cell subprocess.CellSpec) bool {
	if cell.Strategy == subprocess.CellStrategyPackedGo {
		return true
	}
	if cell.Strategy == subprocess.CellStrategyIsolated && len(cell.Extensions) == 1 {
		cfg := cell.Extensions[0]
		return cfg.EntrypointKind == "factory" && cfg.RuntimeLanguage == "go"
	}
	return false
}

// collectFused maps a Go-factory cell's extensions to fuse entries (no writes).
func collectFused(cell subprocess.CellSpec) ([]fusedEntry, error) {
	for _, cfg := range cell.Extensions {
		if cfg.SDKName == extsource.LegacyGoSDKModulePath {
			return nil, fmt.Errorf("fuse %q: legacy SDK module %s cannot register in-process; import %s", cfg.Name, cfg.SDKName, extsource.GoSDKModulePath)
		}
	}
	goExts, err := goExtensions(cell)
	if err != nil {
		return nil, err
	}
	out := make([]fusedEntry, 0, len(goExts))
	for _, ge := range goExts {
		if ge.Root == "" {
			return nil, fmt.Errorf("fuse %q: no source dir", ge.Name)
		}
		if ge.Factory != "Extension" {
			return nil, fmt.Errorf("fuse %q: factory must be Extension", ge.Name)
		}
		out = append(out, fusedEntry{
			Name:             ge.Name,
			Pkg:              runtimecell.ExtPackageName(ge.Name),
			Factory:          ge.Factory,
			Root:             ge.Root,
			ModulePath:       ge.ModulePath,
			Package:          ge.Package,
			WorkspaceModules: append([]string(nil), ge.WorkspaceModules...),
		})
	}
	return out, nil
}

// overlayFuse imports each extension factory through its real package path and
// pins local module roots in the build's view of Pig's go.mod.
func overlayFuse(o *buildOverlay, sourceRoot string, fused []fusedEntry) error {
	fuseDir := filepath.Join(sourceRoot, "coding", "extension", "host", "fusepack")
	genPath := filepath.Join(fuseDir, "registry_generated.go")
	goModPath := filepath.Join(sourceRoot, "go.mod")

	originalGoMod, err := os.ReadFile(goModPath)
	if err != nil {
		return fmt.Errorf("read Pig go.mod: %w", err)
	}
	parsed, err := modfile.Parse(goModPath, originalGoMod, nil)
	if err != nil {
		return fmt.Errorf("parse Pig go.mod: %w", err)
	}
	for _, f := range fused {
		if f.ModulePath == "" || f.Package == "" {
			return fmt.Errorf("fuse %q: module and package paths are required", f.Name)
		}
		if err := parsed.AddRequire(f.ModulePath, "v0.0.0"); err != nil {
			return fmt.Errorf("fuse %q require: %w", f.Name, err)
		}
		if err := parsed.AddReplace(f.ModulePath, "", f.Root, ""); err != nil {
			return fmt.Errorf("fuse %q replace: %w", f.Name, err)
		}
		for _, moduleRoot := range f.WorkspaceModules {
			moduleData, err := os.ReadFile(filepath.Join(moduleRoot, "go.mod"))
			if err != nil {
				return fmt.Errorf("fuse %q workspace module: %w", f.Name, err)
			}
			moduleFile, err := modfile.Parse(filepath.Join(moduleRoot, "go.mod"), moduleData, nil)
			if err != nil || moduleFile.Module == nil {
				return fmt.Errorf("fuse %q workspace module %s has no module path", f.Name, moduleRoot)
			}
			modulePath := moduleFile.Module.Mod.Path
			if modulePath == f.ModulePath {
				continue
			}
			if err := parsed.AddRequire(modulePath, "v0.0.0"); err != nil {
				return err
			}
			if err := parsed.AddReplace(modulePath, "", moduleRoot, ""); err != nil {
				return err
			}
		}
	}
	formatted, err := parsed.Format()
	if err != nil {
		return err
	}
	if err := o.bytes(goModPath, formatted); err != nil {
		return err
	}
	return o.bytes(genPath, []byte(renderFuseRegistry(fused)))
}

func renderFuseRegistry(fused []fusedEntry) string {
	var b strings.Builder
	b.WriteString("// Code generated by pig piglet build. DO NOT EDIT.\npackage fusepack\n\nimport (\n")
	for _, f := range fused {
		_, _ = fmt.Fprintf(&b, "\t%s %q\n", f.Pkg, f.Package)
	}
	b.WriteString(")\n\nfunc init() {\n")
	for _, f := range fused {
		_, _ = fmt.Fprintf(&b, "\tregisterFactory(%q, %s.%s)\n", f.Name, f.Pkg, f.Factory)
	}
	b.WriteString("}\n")
	return b.String()
}

// sourceBuildEnv builds Pig from a source checkout through that checkout's
// go.work when it has one, even if the caller exported GOWORK=off. Pig's root
// go.mod requires extensions/sdk at its release version without a replace
// (so `go install .../cmd/pig@version` works); only go.work resolves it to the
// checkout's own SDK. Workspace mode accepts only -mod=readonly or vendor, so
// a -mod=mod in GOFLAGS is dropped.
func sourceBuildEnv(sourceRoot string, env []string) []string {
	work := filepath.Join(sourceRoot, "go.work")
	if _, err := os.Stat(work); err != nil {
		return env
	}
	out := make([]string, 0, len(env)+2)
	goflags := ""
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "GOWORK="):
		case strings.HasPrefix(kv, "GOFLAGS="):
			var kept []string
			for f := range strings.FieldsSeq(strings.TrimPrefix(kv, "GOFLAGS=")) {
				if f != "-mod=mod" {
					kept = append(kept, f)
				}
			}
			goflags = strings.Join(kept, " ")
		default:
			out = append(out, kv)
		}
	}
	return append(out, "GOWORK="+work, "GOFLAGS="+goflags)
}
