package subprocess

import (
	"path/filepath"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

// CurrentCacheEntries resolves the cache entries selected by the same source
// hashes and cell planner used during startup. It performs no build and returns
// only complete entries that already exist.
func CurrentCacheEntries(configs []ExtConfig, cacheRoot string) (map[string]struct{}, []error) {
	current := make(map[string]struct{})
	resolved := append([]ExtConfig(nil), configs...)
	for i := range resolved {
		if resolved[i].Source == "" || resolved[i].ContentHash != "" {
			continue
		}
		buildType, err := detectBuildType(resolved[i].Source)
		if err != nil {
			continue
		}
		hash, err := hashSourceDir(resolved[i].Source, buildType)
		if err == nil {
			resolved[i].ContentHash = hash
		}
	}
	builder := NewBuilder(filepath.Join(cacheRoot, "ext"))
	var errs []error
	for _, cell := range PlanCells(resolved, nil) {
		var entry string
		var valid bool
		var err error
		switch cell.Strategy {
		case CellStrategyIsolated:
			cfg := cell.Extensions[0]
			switch {
			case isGoFactoryConfig(cfg):
				var ext runtimecell.GoExtension
				ext, err = goExtensionFromConfig(cfg)
				if err == nil {
					entry, valid, err = runtimecell.CurrentGoPackedCellEntry(cacheRoot, cell.Key, []runtimecell.GoExtension{ext})
				}
			case isRustFactoryConfig(cfg):
				var ext runtimecell.RustExtension
				ext, err = rustExtensionFromConfig(cfg)
				if err == nil {
					entry, valid, err = runtimecell.CurrentRustPackedCellEntry(cacheRoot, cell.Key, []runtimecell.RustExtension{ext})
				}
			case isPythonFactoryConfig(cfg):
				var ext runtimecell.PythonExtension
				ext, err = pythonExtensionFromConfig(cfg)
				if err == nil {
					entry, valid, err = runtimecell.CurrentPythonPackedCellEntry(cacheRoot, cell.Key, []runtimecell.PythonExtension{ext})
				}
			case cfg.Source != "":
				entry, valid, err = builder.CurrentCacheEntry(cfg.Name, cfg.Source)
			}
		case CellStrategyPackedGo:
			exts := make([]runtimecell.GoExtension, 0, len(cell.Extensions))
			for _, cfg := range cell.Extensions {
				ext, convertErr := goExtensionFromConfig(cfg)
				if convertErr != nil {
					err = convertErr
					break
				}
				exts = append(exts, ext)
			}
			if err == nil {
				entry, valid, err = runtimecell.CurrentGoPackedCellEntry(cacheRoot, cell.Key, exts)
			}
		case CellStrategyPackedRust:
			exts := make([]runtimecell.RustExtension, 0, len(cell.Extensions))
			for _, cfg := range cell.Extensions {
				ext, convertErr := rustExtensionFromConfig(cfg)
				if convertErr != nil {
					err = convertErr
					break
				}
				exts = append(exts, ext)
			}
			if err == nil {
				entry, valid, err = runtimecell.CurrentRustPackedCellEntry(cacheRoot, cell.Key, exts)
			}
		case CellStrategyPackedPython:
			exts := make([]runtimecell.PythonExtension, 0, len(cell.Extensions))
			for _, cfg := range cell.Extensions {
				ext, convertErr := pythonExtensionFromConfig(cfg)
				if convertErr != nil {
					err = convertErr
					break
				}
				exts = append(exts, ext)
			}
			if err == nil {
				entry, valid, err = runtimecell.CurrentPythonPackedCellEntry(cacheRoot, cell.Key, exts)
			}
		case CellStrategyPackedNode:
			exts, convertErr := cell.NodeExtensions()
			if convertErr != nil {
				err = convertErr
			} else {
				entry, valid, err = CurrentNodePackedCellEntry(cacheRoot, cell.Key, exts)
			}
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if valid {
			current[filepath.Clean(entry)] = struct{}{}
		}
	}
	return current, errs
}
