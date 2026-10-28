package subprocess

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

// errNoLispSupport is what a Lisp extension fails with in a program that never called EnableLisp.
var errNoLispSupport = errors.New("this program was built without Lisp extension support")

// lisp holds the Lisp support EnableLisp installs. The host reaches the SBCL image build, its cache lookup and the standalone launcher only
// through it, and only EnableLisp refers to the code behind it, so the linker drops that code, and the Quicklisp resolver the build uses, from a
// program that never calls EnableLisp. cmd/pig and the programs that run Lisp extensions import the lispcell package, which calls it.
var lisp struct {
	stage   func(h *Host, ctx context.Context, cell CellSpec, oldByName map[string]*managedExt, rep *ReloadCellReport) ([]stagedManagedExt, error)
	current func(cacheRoot string, cell CellSpec) (string, bool, error)
	command func(h *Host, ctx, extCtx context.Context, language, binPath string) (*exec.Cmd, error)
	// hasSource reports whether a source directory holds a Lisp extension.
	hasSource func(dir string) bool
}

// EnableLisp lets the host build, find and start Lisp extensions. It is called from the init function of the lispcell package.
func EnableLisp() {
	lisp.stage = stageLispCell
	lisp.current = currentLispEntry
	lisp.command = lispImageCommand
	lisp.hasSource = hasLispSource
}

// stageLisp builds and starts the runner image of a Lisp cell: a packed cell or the one-member cell of an isolated factory.
func (h *Host) stageLisp(ctx context.Context, cell CellSpec, oldByName map[string]*managedExt, rep *ReloadCellReport) ([]stagedManagedExt, error) {
	if lisp.stage == nil {
		return nil, errNoLispSupport
	}
	return lisp.stage(h, ctx, cell, oldByName, rep)
}

// lispCurrentEntry returns the valid cache entry of a Lisp cell without building it.
func lispCurrentEntry(cacheRoot string, cell CellSpec) (string, bool, error) {
	if lisp.current == nil {
		return "", false, errNoLispSupport
	}
	return lisp.current(cacheRoot, cell)
}

// lispCommand returns the command that runs binPath when it is an exact Lisp standalone, and nil for any other extension.
func (h *Host) lispCommand(ctx, extCtx context.Context, language, binPath string) (*exec.Cmd, error) {
	if lisp.command == nil {
		return nil, nil
	}
	return lisp.command(h, ctx, extCtx, language, binPath)
}

// lispImageCommand runs an exact Lisp standalone in the cached SDK image, which loads the script named on its command line, and returns nil for any other extension.
// pig additive (D19): the standalone form of a Lisp extension needs the SDK image.
func lispImageCommand(h *Host, ctx, extCtx context.Context, language, binPath string) (*exec.Cmd, error) {
	if language != "lisp" && !strings.HasSuffix(strings.ToLower(binPath), ".lisp") {
		return nil, nil
	}
	image, err := runtimecell.BuildLispBaseImage(ctx, filepath.Dir(h.builder.cacheDir))
	if err != nil {
		return nil, fmt.Errorf("build the Lisp image for %s: %w", binPath, err)
	}
	return linkerexec.CommandContext(extCtx, image, binPath), nil
}

func stageLispCell(h *Host, ctx context.Context, cell CellSpec, oldByName map[string]*managedExt, rep *ReloadCellReport) ([]stagedManagedExt, error) {
	if cell.Strategy == CellStrategyIsolated {
		return h.stageIsolatedLispFactory(ctx, cell.Extensions[0], oldByName, rep)
	}
	return h.stagePackedLisp(ctx, cell, oldByName, rep)
}

func currentLispEntry(cacheRoot string, cell CellSpec) (string, bool, error) {
	exts := make([]runtimecell.LispExtension, 0, len(cell.Extensions))
	for _, cfg := range cell.Extensions {
		ext, err := lispExtensionFromConfig(cfg)
		if err != nil {
			return "", false, err
		}
		exts = append(exts, ext)
	}
	return runtimecell.CurrentLispPackedCellEntry(cacheRoot, cell.Key, exts)
}

func (h *Host) stagePackedLisp(ctx context.Context, cell CellSpec, oldByName map[string]*managedExt, rep *ReloadCellReport) ([]stagedManagedExt, error) {
	lispExts, err := cell.LispExtensions()
	if err != nil {
		return nil, err
	}
	cacheRoot := filepath.Dir(h.builder.cacheDir)
	h.markCellExtensions(cell, "build-check-start")
	packed, err := runtimecell.BuildLispPackedCell(ctx, cacheRoot, cell.Key, lispExts)
	if err != nil {
		return nil, fmt.Errorf("build packed Lisp cell %s: %w", cell.Key, err)
	}
	h.markCellExtensions(cell, "build-check-done")
	rep.Hash = packed.Hash
	rep.BinaryPath = packed.BinaryPath
	rep.Cached = packed.Cached
	rep.BuildDuration = packed.BuildDuration
	staged, _, err := h.startLispPackedCell(ctx, packed)
	if err != nil {
		if acceptErr, ok := errors.AsType[*packedAcceptError](err); ok {
			rep.Replaced = anyExisting(cell.Extensions, oldByName)
			return staged, acceptErr
		}
		return nil, fmt.Errorf("reload packed Lisp cell %s: %w", cell.Key, err)
	}
	rep.Replaced = anyExisting(cell.Extensions, oldByName)
	return staged, nil
}

func (h *Host) stageIsolatedLispFactory(ctx context.Context, cfg ExtConfig, oldByName map[string]*managedExt, rep *ReloadCellReport) ([]stagedManagedExt, error) {
	lispExt, err := lispExtensionFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	cacheRoot := filepath.Dir(h.builder.cacheDir)
	cellKey := isolatedCell(cfg, "factory").Key
	h.markExtension(cfg.Name, "build-check-start")
	packed, err := runtimecell.BuildLispPackedCell(ctx, cacheRoot, cellKey, []runtimecell.LispExtension{lispExt})
	if err != nil {
		return nil, fmt.Errorf("build isolated Lisp factory %s: %w", cfg.Name, err)
	}
	h.markExtension(cfg.Name, "build-check-done")
	rep.Hash = packed.Hash
	rep.BinaryPath = packed.BinaryPath
	rep.Cached = packed.Cached
	rep.BuildDuration = packed.BuildDuration
	old := oldByName[cfg.Name]
	staged, _, err := h.startLispPackedCell(ctx, packed)
	if err != nil {
		return nil, fmt.Errorf("reload isolated Lisp factory %s: %w", cfg.Name, err)
	}
	rep.Replaced = old != nil
	return staged, nil
}

// LispExtensions lists the members of a packed-lisp cell.
func (c CellSpec) LispExtensions() ([]runtimecell.LispExtension, error) {
	if c.Strategy != CellStrategyPackedLisp {
		return nil, fmt.Errorf("cell %s is %s, not packed-lisp", c.Key, c.Strategy)
	}
	out := make([]runtimecell.LispExtension, 0, len(c.Extensions))
	for _, cfg := range c.Extensions {
		ext, err := lispExtensionFromConfig(cfg)
		if err != nil {
			return nil, err
		}
		out = append(out, ext)
	}
	return out, nil
}

func lispExtensionFromConfig(cfg ExtConfig) (runtimecell.LispExtension, error) {
	if !isLispFactoryConfig(cfg) {
		return runtimecell.LispExtension{}, fmt.Errorf("extension %q is not a Lisp factory extension", cfg.Name)
	}
	return runtimecell.LispExtension{
		Name:    cfg.Name,
		Root:    cfg.Source,
		Package: cfg.Package,
		Factory: cfg.Factory,
		Hash:    valueOr(cfg.ContentHash, cfg.Source),
		Verify:  cfg.sourceCheck,
	}, nil
}

// LoadLispPackedCell starts a generated Lisp runner image and registers every contained extension
// atomically. It mirrors the Python path and keeps the current subprocess wire with one socket and
// one register handshake per extension.
func (h *Host) LoadLispPackedCell(ctx context.Context, cell *runtimecell.LispPackedCell) ([]extension.Extension, error) {
	staged, registered, err := h.startLispPackedCell(ctx, cell)
	if err != nil {
		if _, ok := errors.AsType[*packedAcceptError](err); ok {
			h.rollbackPartialPackedCell(staged)
		}
		return nil, err
	}
	h.commitStaged(staged, nil, "packed replaced")
	return registered, nil
}

func (h *Host) startLispPackedCell(ctx context.Context, cell *runtimecell.LispPackedCell) ([]stagedManagedExt, []extension.Extension, error) {
	startCell, err := lispStartCell(cell)
	if err != nil {
		return nil, nil, err
	}
	return h.startGoPackedCell(ctx, startCell)
}

func lispStartCell(cell *runtimecell.LispPackedCell) (*runtimecell.GoPackedCell, error) {
	if cell == nil {
		return nil, newLoadError("packed-lisp-cell", "resolve", "missing_cell", fmt.Errorf("nil packed Lisp cell"))
	}
	start := &runtimecell.GoPackedCell{
		Key:        cell.Key,
		Hash:       cell.Hash,
		CacheDir:   cell.CacheDir,
		BinaryPath: cell.BinaryPath,
		Cached:     cell.Cached,
		Extensions: make([]runtimecell.GoExtension, 0, len(cell.Extensions)),
	}
	for _, ext := range cell.Extensions {
		start.Extensions = append(start.Extensions, runtimecell.GoExtension{Name: ext.Name, Root: ext.Root})
	}
	return start, nil
}
