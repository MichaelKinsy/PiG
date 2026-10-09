package subprocess

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

// EmbeddedCell is a source-free packed cell extracted from a Piglet Binary binary.
// It is intentionally independent of the coding/extension/host/cellpack package so the
// generic subprocess host does not depend on Piglet Binary build orchestration.
// A Node cell is the exception to source-free: its BinaryPath is the directory
// holding its members' extracted sources.
type EmbeddedCell struct {
	Language   string
	Key        string
	Strategy   string
	BinaryPath string
	Extensions []EmbeddedExtension
}

type EmbeddedExtension struct {
	Name string
	Hash string
	// Source is a Node member's source path, in slash form, relative to its
	// cell's BinaryPath.
	Source string
}

// LoadEmbeddedCells starts cells already embedded in a Piglet Binary binary. It does
// not touch source directories or invoke language toolchains, except that a
// Node cell's members start from their extracted sources with the user's
// installed Node, exactly as Stock PiG starts the same extensions.
func (h *Host) LoadEmbeddedCells(ctx context.Context, cells []EmbeddedCell) ([]extension.Extension, []error) {
	h.mu.Lock()
	h.embeddedCells = cloneEmbeddedCells(cells)
	h.mu.Unlock()
	h.recordLoadOrder(embeddedCellConfigs(cells), false)

	nodeConfigs, cells := embeddedNodeConfigs(cells)
	var loaded []extension.Extension
	var errs []error
	for _, cell := range cells {
		staged, registered, err := h.stageEmbeddedCell(ctx, cell)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		h.commitStaged(staged, nil, "embedded Piglet Binary cell")
		loaded = append(loaded, registered...)
	}
	if len(nodeConfigs) > 0 {
		errs = append(errs, unresolvedLoadErrors(nodeConfigs)...)
		resolved := slices.DeleteFunc(nodeConfigs, func(cfg ExtConfig) bool { return cfg.resolveErr != nil })
		started, startErrs := h.startPlannedCells(ctx, resolved)
		loaded = append(loaded, started...)
		errs = append(errs, startErrs...)
	}
	h.sortExtensionsByLoadOrder(loaded)
	return loaded, errs
}

// embeddedNodeConfigs resolves the members of the Node cells among cells from
// their extracted sources, as Stock PiG resolves the same extensions, so the
// host plans, starts, and reloads them through its ordinary Node cell path. It
// returns those configs and a copy of the other cells. A member whose source
// does not resolve is an unresolved config that reports the failure.
// pig additive (D18): a Piglet Binary runs its Node extensions as Stock PiG does.
func embeddedNodeConfigs(cells []EmbeddedCell) ([]ExtConfig, []EmbeddedCell) {
	var configs []ExtConfig
	rest := make([]EmbeddedCell, 0, len(cells))
	for _, cell := range cells {
		if cell.Language != "node" {
			rest = append(rest, cell)
			continue
		}
		for _, member := range cell.Extensions {
			source := filepath.Join(cell.BinaryPath, filepath.FromSlash(member.Source))
			cfg, _, err := ResolveExtConfigWithIdentity(source, member.Name)
			if err != nil {
				cfg = UnresolvedExtConfig(source, err)
			}
			configs = append(configs, cfg)
		}
	}
	return configs, cloneEmbeddedCells(rest)
}

// embeddedLoadOrder lists the embedded cells' members in manifest order, as
// LoadEmbeddedCells ranks them, taking each Node member's config from
// nodeConfigs, which holds them in the order embeddedNodeConfigs returned them.
// pig additive (D18): a reload keeps a Piglet Binary's extension order.
func embeddedLoadOrder(cells []EmbeddedCell, nodeConfigs []ExtConfig) []ExtConfig {
	order := make([]ExtConfig, 0, len(cells)+len(nodeConfigs))
	for _, cell := range cells {
		if cell.Language != "node" {
			order = append(order, embeddedCellConfigs([]EmbeddedCell{cell})...)
			continue
		}
		members := min(len(cell.Extensions), len(nodeConfigs))
		order = append(order, nodeConfigs[:members]...)
		nodeConfigs = nodeConfigs[members:]
	}
	return order
}

func cloneEmbeddedCells(cells []EmbeddedCell) []EmbeddedCell {
	cloned := make([]EmbeddedCell, len(cells))
	for i := range cells {
		cloned[i] = cells[i]
		cloned[i].Extensions = append([]EmbeddedExtension(nil), cells[i].Extensions...)
	}
	return cloned
}

func embeddedCellConfigs(cells []EmbeddedCell) []ExtConfig {
	var configs []ExtConfig
	for _, cell := range cells {
		for _, embedded := range cell.Extensions {
			configs = append(configs, ExtConfig{
				Name:            embedded.Name,
				Path:            cell.BinaryPath,
				Enabled:         true,
				RuntimeKind:     "subprocess",
				RuntimeLanguage: cell.Language,
			})
		}
	}
	return configs
}

func (h *Host) stageEmbeddedCell(ctx context.Context, cell EmbeddedCell) ([]stagedManagedExt, []extension.Extension, error) {
	if cell.BinaryPath == "" {
		return nil, nil, fmt.Errorf("embedded cell %q: missing binary path", cell.Key)
	}
	if len(cell.Extensions) == 0 {
		return nil, nil, fmt.Errorf("embedded cell %q: no extensions", cell.Key)
	}
	strategy := cell.Strategy
	if strategy == "" {
		strategy = string(CellStrategyPackedGo)
	}
	if strategy == string(CellStrategyIsolated) {
		if len(cell.Extensions) != 1 {
			return nil, nil, fmt.Errorf("embedded isolated cell %q: got %d extensions", cell.Key, len(cell.Extensions))
		}
		cfg := ExtConfig{
			Name:            cell.Extensions[0].Name,
			Path:            cell.BinaryPath,
			Enabled:         true,
			RuntimeKind:     "subprocess",
			RuntimeLanguage: cell.Language,
		}
		managed, ext, err := h.startManaged(ctx, cfg)
		if err != nil {
			return nil, nil, err
		}
		return []stagedManagedExt{{name: cfg.Name, me: managed}}, []extension.Extension{*ext}, nil
	}
	switch strategy {
	case string(CellStrategyPackedGo):
		exts := make([]runtimecell.GoExtension, len(cell.Extensions))
		for i, e := range cell.Extensions {
			exts[i] = runtimecell.GoExtension{Name: e.Name, Hash: e.Hash}
		}
		packed := &runtimecell.GoPackedCell{Key: cell.Key, Hash: "embedded:" + filepath.Base(cell.BinaryPath), CacheDir: filepath.Dir(cell.BinaryPath), BinaryPath: cell.BinaryPath, Cached: true, Extensions: exts}
		staged, registered, err := h.startGoPackedCell(ctx, packed)
		if err != nil {
			h.rollbackEmbeddedAcceptError(staged, err)
			return nil, nil, err
		}
		return staged, registered, nil
	case string(CellStrategyPackedRust):
		exts := make([]runtimecell.RustExtension, len(cell.Extensions))
		for i, e := range cell.Extensions {
			exts[i] = runtimecell.RustExtension{Name: e.Name, Hash: e.Hash}
		}
		packed := &runtimecell.RustPackedCell{Key: cell.Key, Hash: "embedded:" + filepath.Base(cell.BinaryPath), CacheDir: filepath.Dir(cell.BinaryPath), BinaryPath: cell.BinaryPath, Cached: true, Extensions: exts}
		staged, registered, err := h.startRustPackedCell(ctx, packed)
		if err != nil {
			h.rollbackEmbeddedAcceptError(staged, err)
			return nil, nil, err
		}
		return staged, registered, nil
	case string(CellStrategyPackedPython):
		exts := make([]runtimecell.PythonExtension, len(cell.Extensions))
		for i, e := range cell.Extensions {
			exts[i] = runtimecell.PythonExtension{Name: e.Name, Hash: e.Hash}
		}
		packed := &runtimecell.PythonPackedCell{Key: cell.Key, Hash: "embedded:" + filepath.Base(cell.BinaryPath), CacheDir: filepath.Dir(cell.BinaryPath), BinaryPath: cell.BinaryPath, Cached: true, Extensions: exts}
		staged, registered, err := h.startPythonPackedCell(ctx, packed)
		if err != nil {
			h.rollbackEmbeddedAcceptError(staged, err)
			return nil, nil, err
		}
		return staged, registered, nil
	default:
		return nil, nil, fmt.Errorf("embedded cell %q: unsupported strategy %q", cell.Key, cell.Strategy)
	}
}

// rollbackEmbeddedAcceptError tears down staged down to match
// LoadEmbeddedCells's atomic per-cell contract: it discards staged
// unconditionally on any error (never calls commitStaged for a failed
// cell), so a packedAcceptError's partial success (kept alive by
// startGoPackedCell/startRustPackedCell/startPythonPackedCell for a
// planner caller that wants it) must be torn down here or it leaks a live,
// accepted, but never-committed connection and process.
func (h *Host) rollbackEmbeddedAcceptError(staged []stagedManagedExt, err error) {
	if _, ok := errors.AsType[*packedAcceptError](err); ok {
		h.rollbackPartialPackedCell(staged)
	}
}
