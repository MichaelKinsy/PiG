package subprocess

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

// stageOutcome is the result of staging one planner cell. ReloadCellReport is
// populated for every attempted cell so /reload --explain reflects the full
// planner decision.
type stageOutcome struct {
	staged []stagedManagedExt
	report ReloadCellReport
}

func cellExtNames(cell CellSpec) []string {
	names := make([]string, 0, len(cell.Extensions))
	for _, cfg := range cell.Extensions {
		names = append(names, cfg.Name)
	}
	return names
}

func (h *Host) markCellExtensions(cell CellSpec, phase string) {
	for _, cfg := range cell.Extensions {
		h.markExtension(cfg.Name, phase)
	}
}

func reasonFor(cell CellSpec) string {
	switch cell.Strategy {
	case CellStrategyIsolated:
		if len(cell.Extensions) == 1 {
			cfg := cell.Extensions[0]
			switch {
			case cfg.EntrypointKind == "factory":
				return "factory isolated (strict or unpackable)"
			case cfg.RuntimeKind == "subprocess":
				return "isolated subprocess (source/command)"
			default:
				return "isolated"
			}
		}
		return "isolated"
	case CellStrategyPackedGo:
		return "go factory packed (shared-ok)"
	case CellStrategyPackedRust:
		return "rust factory packed (shared-ok)"
	case CellStrategyPackedPython:
		return "python factory packed (shared-ok)"
	case CellStrategyPackedNode:
		return "node factory packed (shared-ok)"
	default:
		return string(cell.Strategy)
	}
}

// cellFailure is one extension that failed to stage.
type cellFailure struct {
	cfg ExtConfig
	err error
}

// stageCellIsolating stages cell and returns every staging attempt with the
// extensions that failed. Upstream loads each extension on its own and records
// a failure for that extension only. When a packed cell fails, each member is
// therefore staged again in its own isolated cell, so one failing member does
// not take its packed siblings down.
func (h *Host) stageCellIsolating(ctx context.Context, cell CellSpec, oldByName map[string]*managedExt) ([]stageOutcome, []cellFailure) {
	outcome, err := h.stageCell(ctx, cell, oldByName)
	return h.isolateCellFailure(ctx, cell, oldByName, outcome, err)
}

// isolateCellFailure preserves successful staged outcomes and retries each
// member of a failed packed cell in isolation. Callers that prepared the packed
// cell concurrently can reuse its outcome without repeating that preparation.
func (h *Host) isolateCellFailure(ctx context.Context, cell CellSpec, oldByName map[string]*managedExt, outcome stageOutcome, err error) ([]stageOutcome, []cellFailure) {
	if err == nil {
		return []stageOutcome{outcome}, nil
	}
	if acceptErr, ok := errors.AsType[*packedAcceptError](err); ok {
		// One or more members failed to register after the shared process
		// was already spawned; outcome.staged already holds every member
		// that succeeded (packedAcceptError's doc comment on
		// startGoPackedCell). Report exactly the named failures and skip
		// the isolated-retry loop below entirely: retrying would silently
		// re-invoke an already-succeeded sibling's factory a second time,
		// breaking LoadFinalExtensionSet's "loaded before trust extensions
		// are reused exactly once" contract.
		failures := make([]cellFailure, 0, len(acceptErr.perMember))
		for _, cfg := range cell.Extensions {
			if memberErr, failed := acceptErr.perMember[cfg.Name]; failed {
				failures = append(failures, cellFailure{cfg: cfg, err: memberErr})
			}
		}
		return []stageOutcome{outcome}, failures
	}
	if len(cell.Extensions) == 1 {
		return []stageOutcome{outcome}, []cellFailure{{cfg: cell.Extensions[0], err: err}}
	}
	outcomes := []stageOutcome{outcome}
	var failures []cellFailure
	for _, cfg := range cell.Extensions {
		member, memberErr := h.stageCell(ctx, isolatedCell(cfg, "packed cell failed"), oldByName)
		outcomes = append(outcomes, member)
		if memberErr != nil {
			failures = append(failures, cellFailure{cfg: cfg, err: memberErr})
		}
	}
	return outcomes, failures
}

func (h *Host) stageCell(ctx context.Context, cell CellSpec, oldByName map[string]*managedExt) (stageOutcome, error) {
	work := func() (stageOutcome, error) {
		rep := ReloadCellReport{
			Key:        cell.Key,
			Strategy:   cell.Strategy,
			Language:   cell.Language,
			Extensions: cellExtNames(cell),
			Reason:     reasonFor(cell),
		}
		switch cell.Strategy {
		case CellStrategyIsolated:
			if len(cell.Extensions) != 1 {
				return stageOutcome{}, fmt.Errorf("isolated cell %s has %d extensions", cell.Key, len(cell.Extensions))
			}
			staged, err := h.stageIsolated(ctx, cell.Extensions[0], oldByName, &rep)
			return stageOutcome{staged: staged, report: rep}, err
		case CellStrategyPackedGo:
			staged, err := h.stagePackedGo(ctx, cell, oldByName, &rep)
			return stageOutcome{staged: staged, report: rep}, err
		case CellStrategyPackedRust:
			staged, err := h.stagePackedRust(ctx, cell, oldByName, &rep)
			return stageOutcome{staged: staged, report: rep}, err
		case CellStrategyPackedPython:
			staged, err := h.stagePackedPython(ctx, cell, oldByName, &rep)
			return stageOutcome{staged: staged, report: rep}, err
		case CellStrategyPackedNode:
			staged, err := h.stagePackedNode(ctx, cell, oldByName, &rep)
			return stageOutcome{staged: staged, report: rep}, err
		default:
			return stageOutcome{}, fmt.Errorf("unsupported cell strategy %q", cell.Strategy)
		}
	}
	// Packed Python runners import the staged SDK when the subprocess starts, so
	// keep its lease through registration. Go and Rust consume staged sources
	// only while compiling; their builders own a narrower lease and must not
	// hold the global transaction lock while a subprocess registers.
	if cell.Language == "python" {
		return pigsdklock.WithBuild(ctx, h.builder.configRoot, work)
	}
	return work()
}

func (h *Host) stageIsolated(ctx context.Context, cfg ExtConfig, oldByName map[string]*managedExt, rep *ReloadCellReport) ([]stagedManagedExt, error) {
	if isGoFactoryConfig(cfg) {
		return h.stageIsolatedGoFactory(ctx, cfg, oldByName, rep)
	}
	if isRustFactoryConfig(cfg) {
		return h.stageIsolatedRustFactory(ctx, cfg, oldByName, rep)
	}
	if isPythonFactoryConfig(cfg) {
		return h.stageIsolatedPythonFactory(ctx, cfg, oldByName, rep)
	}
	resolved := cfg
	h.markExtension(cfg.Name, "build-check-start")
	if resolved.Source != "" && resolved.Path == "" {
		result, err := h.builder.Build(resolved.Name, resolved.Source)
		if err != nil {
			return nil, fmt.Errorf("build %q: %w", resolved.Name, err)
		}
		resolved.Path = result.BinaryPath
	}
	h.markExtension(cfg.Name, "build-check-done")
	rep.BinaryPath = resolved.Path
	// Upstream reload invokes every extension factory again, so an unchanged
	// extension still gets a fresh instance: only its build artifact is reused.
	// pig divergence (D70): the fresh instance is a fresh process, so module-level
	// state is re-initialized even where Pi's module cache would have kept it.
	old := oldByName[resolved.Name]
	me, _, err := h.startManaged(ctx, resolved)
	if err != nil {
		return nil, fmt.Errorf("reload %q: %w", resolved.Name, err)
	}
	rep.Replaced = old != nil
	return []stagedManagedExt{{name: resolved.Name, me: me}}, nil
}

func (h *Host) stageIsolatedGoFactory(ctx context.Context, cfg ExtConfig, oldByName map[string]*managedExt, rep *ReloadCellReport) ([]stagedManagedExt, error) {
	goExt, err := goExtensionFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	cacheRoot := filepath.Dir(h.builder.cacheDir)
	cellKey := isolatedCell(cfg, "factory").Key
	h.markExtension(cfg.Name, "build-check-start")
	packed, err := runtimecell.BuildGoPackedCellWithSDKRoot(ctx, cacheRoot, cellKey, []runtimecell.GoExtension{goExt}, filepath.Join(h.builder.configRoot, "state", "pigsdk", "sdk"))
	if err != nil {
		return nil, fmt.Errorf("build isolated factory %s: %w", cfg.Name, err)
	}
	h.markExtension(cfg.Name, "build-check-done")
	rep.Hash = packed.Hash
	rep.BinaryPath = packed.BinaryPath
	rep.Cached = packed.Cached
	rep.BuildDuration = packed.BuildDuration
	old := oldByName[cfg.Name]
	staged, _, err := h.startGoPackedCell(ctx, packed)
	if err != nil {
		return nil, fmt.Errorf("reload isolated factory %s: %w", cfg.Name, err)
	}
	rep.Replaced = old != nil
	return staged, nil
}

func (h *Host) stageIsolatedRustFactory(ctx context.Context, cfg ExtConfig, oldByName map[string]*managedExt, rep *ReloadCellReport) ([]stagedManagedExt, error) {
	rustExt, err := rustExtensionFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	cacheRoot := filepath.Dir(h.builder.cacheDir)
	cellKey := isolatedCell(cfg, "factory").Key
	h.markExtension(cfg.Name, "build-check-start")
	packed, err := runtimecell.BuildRustPackedCell(ctx, cacheRoot, cellKey, []runtimecell.RustExtension{rustExt})
	if err != nil {
		return nil, fmt.Errorf("build isolated Rust factory %s: %w", cfg.Name, err)
	}
	h.markExtension(cfg.Name, "build-check-done")
	rep.Hash = packed.Hash
	rep.BinaryPath = packed.BinaryPath
	rep.Cached = packed.Cached
	rep.BuildDuration = packed.BuildDuration
	old := oldByName[cfg.Name]
	staged, _, err := h.startRustPackedCell(ctx, packed)
	if err != nil {
		return nil, fmt.Errorf("reload isolated Rust factory %s: %w", cfg.Name, err)
	}
	rep.Replaced = old != nil
	return staged, nil
}

func (h *Host) stageIsolatedPythonFactory(ctx context.Context, cfg ExtConfig, oldByName map[string]*managedExt, rep *ReloadCellReport) ([]stagedManagedExt, error) {
	pyExt, err := pythonExtensionFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	cacheRoot := filepath.Dir(h.builder.cacheDir)
	cellKey := isolatedCell(cfg, "factory").Key
	h.markExtension(cfg.Name, "build-check-start")
	packed, err := runtimecell.BuildPythonPackedCell(ctx, cacheRoot, cellKey, []runtimecell.PythonExtension{pyExt})
	if err != nil {
		return nil, fmt.Errorf("build isolated Python factory %s: %w", cfg.Name, err)
	}
	h.markExtension(cfg.Name, "build-check-done")
	rep.Hash = packed.Hash
	rep.BinaryPath = packed.BinaryPath
	rep.Cached = packed.Cached
	rep.BuildDuration = packed.BuildDuration
	old := oldByName[cfg.Name]
	staged, _, err := h.startPythonPackedCell(ctx, packed)
	if err != nil {
		return nil, fmt.Errorf("reload isolated Python factory %s: %w", cfg.Name, err)
	}
	rep.Replaced = old != nil
	return staged, nil
}

func (h *Host) stagePackedGo(ctx context.Context, cell CellSpec, oldByName map[string]*managedExt, rep *ReloadCellReport) ([]stagedManagedExt, error) {
	goExts, err := cell.GoExtensions()
	if err != nil {
		return nil, err
	}
	cacheRoot := filepath.Dir(h.builder.cacheDir)
	h.markCellExtensions(cell, "build-check-start")
	packed, err := runtimecell.BuildGoPackedCellWithSDKRoot(ctx, cacheRoot, cell.Key, goExts, filepath.Join(h.builder.configRoot, "state", "pigsdk", "sdk"))
	if err != nil {
		return nil, fmt.Errorf("build packed cell %s: %w", cell.Key, err)
	}
	h.markCellExtensions(cell, "build-check-done")
	rep.Hash = packed.Hash
	rep.BinaryPath = packed.BinaryPath
	rep.Cached = packed.Cached
	rep.BuildDuration = packed.BuildDuration
	staged, _, err := h.startGoPackedCell(ctx, packed)
	if err != nil {
		if acceptErr, ok := errors.AsType[*packedAcceptError](err); ok {
			// One or more members failed to register, but the shared
			// process and any already-accepted siblings are already
			// running: keep staged and propagate acceptErr unwrapped, so
			// isolateCellFailure reports exactly the failed member(s)
			// instead of retrying every member (including the ones that
			// already succeeded) in isolation.
			rep.Replaced = anyExisting(cell.Extensions, oldByName)
			return staged, acceptErr
		}
		return nil, fmt.Errorf("reload packed cell %s: %w", cell.Key, err)
	}
	rep.Replaced = anyExisting(cell.Extensions, oldByName)
	return staged, nil
}

func (h *Host) stagePackedRust(ctx context.Context, cell CellSpec, oldByName map[string]*managedExt, rep *ReloadCellReport) ([]stagedManagedExt, error) {
	rustExts, err := cell.RustExtensions()
	if err != nil {
		return nil, err
	}
	cacheRoot := filepath.Dir(h.builder.cacheDir)
	h.markCellExtensions(cell, "build-check-start")
	packed, err := runtimecell.BuildRustPackedCell(ctx, cacheRoot, cell.Key, rustExts)
	if err != nil {
		return nil, fmt.Errorf("build packed Rust cell %s: %w", cell.Key, err)
	}
	h.markCellExtensions(cell, "build-check-done")
	rep.Hash = packed.Hash
	rep.BinaryPath = packed.BinaryPath
	rep.Cached = packed.Cached
	rep.BuildDuration = packed.BuildDuration
	staged, _, err := h.startRustPackedCell(ctx, packed)
	if err != nil {
		if acceptErr, ok := errors.AsType[*packedAcceptError](err); ok {
			rep.Replaced = anyExisting(cell.Extensions, oldByName)
			return staged, acceptErr
		}
		return nil, fmt.Errorf("reload packed Rust cell %s: %w", cell.Key, err)
	}
	rep.Replaced = anyExisting(cell.Extensions, oldByName)
	return staged, nil
}

func (h *Host) stagePackedNode(ctx context.Context, cell CellSpec, oldByName map[string]*managedExt, rep *ReloadCellReport) ([]stagedManagedExt, error) {
	nodeExts, err := cell.NodeExtensions()
	if err != nil {
		return nil, err
	}
	// Claim this attempt's generation before any build/cache work (CNC-002):
	// see startNodePackedCell's doc comment.
	generation := h.nextPackedCellGeneration(cell.Key)
	cacheRoot := filepath.Dir(h.builder.cacheDir)
	h.markCellExtensions(cell, "build-check-start")
	packed, err := buildNodePackedCell(ctx, cacheRoot, cell.Key, nodeExts)
	if err != nil {
		return nil, fmt.Errorf("build packed Node cell %s: %w", cell.Key, err)
	}
	h.markCellExtensions(cell, "build-check-done")
	rep.Hash = packed.Hash
	rep.BinaryPath = packed.BinaryPath
	rep.Cached = packed.Cached
	staged, _, err := h.startNodePackedCell(ctx, packed, generation)
	if err != nil {
		if acceptErr, ok := errors.AsType[*packedAcceptError](err); ok {
			rep.Replaced = anyExisting(cell.Extensions, oldByName)
			return staged, acceptErr
		}
		return nil, fmt.Errorf("reload packed Node cell %s: %w", cell.Key, err)
	}
	rep.Replaced = anyExisting(cell.Extensions, oldByName)
	return staged, nil
}

func (h *Host) stagePackedPython(ctx context.Context, cell CellSpec, oldByName map[string]*managedExt, rep *ReloadCellReport) ([]stagedManagedExt, error) {
	pyExts, err := cell.PythonExtensions()
	if err != nil {
		return nil, err
	}
	cacheRoot := filepath.Dir(h.builder.cacheDir)
	h.markCellExtensions(cell, "build-check-start")
	packed, err := runtimecell.BuildPythonPackedCell(ctx, cacheRoot, cell.Key, pyExts)
	if err != nil {
		return nil, fmt.Errorf("build packed Python cell %s: %w", cell.Key, err)
	}
	h.markCellExtensions(cell, "build-check-done")
	rep.Hash = packed.Hash
	rep.BinaryPath = packed.BinaryPath
	rep.Cached = packed.Cached
	rep.BuildDuration = packed.BuildDuration
	staged, _, err := h.startPythonPackedCell(ctx, packed)
	if err != nil {
		if acceptErr, ok := errors.AsType[*packedAcceptError](err); ok {
			rep.Replaced = anyExisting(cell.Extensions, oldByName)
			return staged, acceptErr
		}
		return nil, fmt.Errorf("reload packed Python cell %s: %w", cell.Key, err)
	}
	rep.Replaced = anyExisting(cell.Extensions, oldByName)
	return staged, nil
}

func anyExisting(cfgs []ExtConfig, oldByName map[string]*managedExt) bool {
	for _, cfg := range cfgs {
		if _, ok := oldByName[cfg.Name]; ok {
			return true
		}
	}
	return false
}

func isGoFactoryConfig(cfg ExtConfig) bool {
	return cfg.RuntimeKind == "subprocess" && cfg.RuntimeLanguage == "go" && cfg.EntrypointKind == "factory" && cfg.Source != "" && cfg.Package != "" && cfg.Factory == "Extension"
}

func isRustFactoryConfig(cfg ExtConfig) bool {
	return cfg.RuntimeKind == "subprocess" && cfg.RuntimeLanguage == "rust" && cfg.EntrypointKind == "factory" && cfg.Source != "" && cfg.Package != "" && cfg.Factory == "new_extension"
}

func isPythonFactoryConfig(cfg ExtConfig) bool {
	return cfg.RuntimeKind == "subprocess" && cfg.RuntimeLanguage == "python" && cfg.EntrypointKind == "factory" && cfg.Source != "" && cfg.Package != "" && cfg.Factory == "new_extension"
}

func goExtensionFromConfig(cfg ExtConfig) (runtimecell.GoExtension, error) {
	if !isGoFactoryConfig(cfg) {
		return runtimecell.GoExtension{}, fmt.Errorf("extension %q is not a Go factory extension", cfg.Name)
	}
	return runtimecell.GoExtension{
		Name:             cfg.Name,
		Root:             cfg.Source,
		ModulePath:       cfg.ModulePath,
		Package:          cfg.Package,
		Factory:          cfg.Factory,
		Hash:             valueOr(cfg.ContentHash, cfg.Source),
		WorkspaceModules: append([]string(nil), cfg.GoWorkspaceModules...),
	}, nil
}

func rustExtensionFromConfig(cfg ExtConfig) (runtimecell.RustExtension, error) {
	if !isRustFactoryConfig(cfg) {
		return runtimecell.RustExtension{}, fmt.Errorf("extension %q is not a Rust factory extension", cfg.Name)
	}
	return runtimecell.RustExtension{
		Name:    cfg.Name,
		Root:    cfg.Source,
		Package: cfg.Package,
		Factory: cfg.Factory,
		Hash:    valueOr(cfg.ContentHash, cfg.Source),
	}, nil
}

func pythonExtensionFromConfig(cfg ExtConfig) (runtimecell.PythonExtension, error) {
	if !isPythonFactoryConfig(cfg) {
		return runtimecell.PythonExtension{}, fmt.Errorf("extension %q is not a Python factory extension", cfg.Name)
	}
	return runtimecell.PythonExtension{
		Name:    cfg.Name,
		Root:    cfg.Source,
		Package: cfg.Package,
		Factory: cfg.Factory,
		Hash:    valueOr(cfg.ContentHash, cfg.Source),
	}, nil
}

// quarantineReports returns synthetic ReloadCellReports for any quarantined
// packed cell hashes that the planner fissioned during this Reload. Used by
// Reload() to make quarantine/fission decisions visible in /reload --explain.
func (h *Host) quarantineReports(now time.Time) []ReloadCellReport {
	q := h.QuarantinedCells()
	if len(q) == 0 {
		return nil
	}
	_ = now
	out := make([]ReloadCellReport, 0, len(q))
	for key, reason := range q {
		out = append(out, ReloadCellReport{
			Key:         key,
			Strategy:    CellStrategyIsolated,
			Quarantined: true,
			Reason:      "fissioned (quarantined): " + reason,
		})
	}
	return out
}
