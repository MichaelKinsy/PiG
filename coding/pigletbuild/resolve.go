package pigletbuild

import (
	"fmt"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
)

// reasonForStrategy explains, in the runtime cell planner's vocabulary, why an
// extension in a given cell cannot fuse. Empty when it can (packed-go).
func reasonForStrategy(strategy subprocess.CellStrategy, language string) string {
	switch strategy {
	case subprocess.CellStrategyPackedGo:
		return ""
	case subprocess.CellStrategyPackedRust:
		return "language:rust"
	case subprocess.CellStrategyPackedPython:
		return "language:python"
	default: // isolated
		if language == "go" {
			return "go: not a packable factory"
		}
		if language != "" && language != "unknown" {
			return "language:" + language
		}
		return "not fusible"
	}
}

// extensionInputsFromCells maps runtime cell-planner output to Piglet Binary planner
// inputs. Fusibility is exactly "landed in a packed-go cell", so PlanCells stays
// the single classifier of record instead of the Piglet Binary planner re-deciding it.
func extensionInputsFromCells(cells []subprocess.CellSpec) []ExtensionInput {
	var out []ExtensionInput
	for _, cell := range cells {
		fusible := cell.Strategy == subprocess.CellStrategyPackedGo
		reason := reasonForStrategy(cell.Strategy, cell.Language)
		if fusible && cell.SDK == extsource.LegacyGoSDKModulePath {
			// Fused registration takes the current SDK's Factory type; a legacy
			// SDK import is a distinct Go type, so the cell stays a subprocess.
			fusible, reason = false, "go: legacy SDK module "+extsource.LegacyGoSDKModulePath
		}
		for _, cfg := range cell.Extensions {
			out = append(out, ExtensionInput{
				Name:             cfg.Name,
				Language:         Language(cell.Language),
				Fusible:          fusible,
				NotFusibleReason: reason,
			})
		}
	}
	return out
}

// resolvePigletCells resolves a piglet's extensions to runtime cells via the
// manifest parser (ResolveExtConfig) and cell planner (PlanCells). Returns the
// cells and any per-extension resolution warnings.
func resolvePigletCells(p *piglet.Piglet) ([]subprocess.CellSpec, []string) {
	resolved, errs := piglet.ResolveExtensions(p)

	var warns []string
	for _, e := range errs {
		warns = append(warns, e.Error())
	}

	var configs []subprocess.ExtConfig
	for _, re := range resolved {
		cfg, _, err := subprocess.ResolveExtConfig(re.Path)
		if err != nil {
			warns = append(warns, fmt.Sprintf("extension %q: %v", re.Entry.Name, err))
			continue
		}
		cfg.Enabled = true
		configs = append(configs, cfg)
	}

	return subprocess.PlanCells(configs, nil), warns
}
