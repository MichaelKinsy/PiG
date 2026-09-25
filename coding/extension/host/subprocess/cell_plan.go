package subprocess

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
)

// CellStrategy describes how a group of extension configs should be started.
type CellStrategy string

const (
	CellStrategyIsolated     CellStrategy = "isolated"
	CellStrategyPackedGo     CellStrategy = "packed-go"
	CellStrategyPackedRust   CellStrategy = "packed-rust"
	CellStrategyPackedPython CellStrategy = "packed-python"
	// CellStrategyPackedNode hosts every packable TS/JS extension in one Node
	// process, the way upstream pi loads all its extensions in one process
	// (N8). Unlike the other packed strategies this needs no compiled runner:
	// the type-stripping loader reads each member's source fresh at start.
	CellStrategyPackedNode CellStrategy = "packed-node"
)

// CellSpec is the runtime planner output consumed by reload/startup code.
// It is intentionally host-level rather than command-level so validation,
// reload, and future install flows can share one grouping contract.
type CellSpec struct {
	Key        string
	Hash       string
	Strategy   CellStrategy
	Runtime    string
	Language   string
	SDK        string
	Isolation  string
	Order      int
	Extensions []ExtConfig
}

// PlanCells groups extension configs into runtime cells. Today explicit
// factory-style Go, Rust, Python, and Node SDK extensions are packable.
// Everything else remains an isolated subprocess. Quarantined packed cell
// keys are fissioned back to isolated cells.
//
// A pack group only ever spans a *contiguous* run of the same packGroupKey
// in configured order (CNEO-001). Upstream loader.ts activates each
// extension strictly in path order (loader.ts:613-627): pig only starts a
// later cell's process once every earlier cell has committed
// (stageCellsInOrder), so activation order across cells matches configured
// order exactly when, and only when, a cell's Order is the index of the
// earliest configured extension that ends up in it *and no other cell sits
// between that extension and the next one configured*. Grouping by key alone
// (ignoring what sits between two same-key extensions) breaks that: [NodeA,
// GoB(isolated), NodeC] would otherwise pack NodeA+NodeC into one cell
// ordered at NodeA's index, activating NodeC (in the same process, same
// install loop) before GoB even though NodeC was configured after GoB. This
// splits at every interleaving boundary instead, giving up packing NodeA
// with NodeC in that case (they land in two separate Node cells, so two Node
// processes) in exchange for exact activation order: the common case, a run
// of same-language extensions actually configured together, still packs
// into one cell/process exactly as before.
// pig additive (D20): packed runtime cells are Pig's downstream-only
// subprocess process-sharing optimizer; each extension still speaks the current subprocess wire.
func PlanCells(configs []ExtConfig, quarantined map[string]string) []CellSpec {
	type indexedConfig struct {
		config ExtConfig
		index  int
	}
	type packRun struct {
		key     string
		first   int
		configs []indexedConfig
	}

	var cells []CellSpec
	flush := func(run *packRun) {
		if run == nil {
			return
		}
		configsOut := make([]ExtConfig, len(run.configs))
		for i := range run.configs {
			configsOut[i] = run.configs[i].config
		}
		cell := packedCellAt(run.key, configsOut, run.first)
		if quarantined != nil && quarantined[cell.Key] != "" {
			for _, entry := range run.configs {
				cells = append(cells, isolatedCellAt(entry.config, "quarantined:"+cell.Key, entry.index))
			}
			return
		}
		cells = append(cells, cell)
	}

	var current *packRun
	for index, cfg := range configs {
		if !cfg.Enabled || cfg.resolveErr != nil {
			continue
		}
		cfg = normalizeUnresolvedNodeConfig(cfg)
		if isPackableGoFactory(cfg) || isPackableRustFactory(cfg) || isPackablePythonFactory(cfg) || isPackableNode(cfg) {
			key := packGroupKey(cfg)
			if current != nil && current.key == key {
				current.configs = append(current.configs, indexedConfig{config: cfg, index: index})
				continue
			}
			flush(current)
			current = &packRun{key: key, first: index, configs: []indexedConfig{{config: cfg, index: index}}}
			continue
		}
		flush(current)
		current = nil
		cells = append(cells, isolatedCellAt(cfg, "not-packable", index))
	}
	flush(current)

	sort.SliceStable(cells, func(i, j int) bool {
		if cells[i].Order != cells[j].Order {
			return cells[i].Order < cells[j].Order
		}
		return cells[i].Key < cells[j].Key
	})
	return cells
}

// isShareableIsolation reports whether an isolation token permits packing into
// a shared runtime cell. Per D20 the only packable value is "shared-ok" (also
// the default when unset); every other token: "isolated"/"required"/"strict"
// and any unrecognized value such as a typo: must fail safe to an isolated
// cell. The earlier opt-out list packed unknown tokens, contradicting the D20
// spec ("isolation != shared-ok → isolated") and silently routing, e.g., an
// "isolation: dedicated" extension into the packer.
func isShareableIsolation(isolation string) bool {
	return isolation == "" || strings.EqualFold(isolation, "shared-ok")
}

func isPackableGoFactory(cfg ExtConfig) bool {
	return isShareableIsolation(cfg.Isolation) &&
		cfg.RuntimeKind == "subprocess" && cfg.RuntimeLanguage == "go" &&
		cfg.EntrypointKind == "factory" && cfg.Source != "" && cfg.Package != "" && cfg.Factory == "Extension"
}

func isPackableRustFactory(cfg ExtConfig) bool {
	return isShareableIsolation(cfg.Isolation) &&
		cfg.RuntimeKind == "subprocess" && cfg.RuntimeLanguage == "rust" &&
		cfg.EntrypointKind == "factory" && cfg.Source != "" && cfg.Package != "" && cfg.Factory == "new_extension"
}

func isPackablePythonFactory(cfg ExtConfig) bool {
	return isShareableIsolation(cfg.Isolation) &&
		cfg.RuntimeKind == "subprocess" && cfg.RuntimeLanguage == "python" &&
		cfg.EntrypointKind == "factory" && cfg.Source != "" && cfg.Package != "" && cfg.Factory == "new_extension"
}

// isPackableNode reports whether cfg is a TS/JS extension the Node cell can
// host. Only the conventional factory form (a default export) packs; a
// standalone Node script (an exact executable) always stays isolated,
// matching every other language's standalone form.
func isPackableNode(cfg ExtConfig) bool {
	return isShareableIsolation(cfg.Isolation) &&
		cfg.RuntimeKind == "subprocess" && cfg.RuntimeLanguage == "node" &&
		cfg.EntrypointKind == "factory" && cfg.Source != ""
}

// normalizeUnresolvedNodeConfig fills RuntimeKind/RuntimeLanguage/EntrypointKind
// for a config a caller never resolved through ResolveExtConfigWithIdentity
// (all three fields left at their zero value, as from a bare
// ExtConfig{Source: path}: every direct Host.LoadAll/Reload caller in this
// package's tests, and any future caller that skips the resolver). Without
// this, packedCellAt cannot tell such a config is a Node factory (it reads
// RuntimeLanguage, not source content) and a bare Node extension would either
// silently join the wrong-language pack group or fall back to one process
// per extension. A config that already carries any of the three fields (the
// normal ResolveExtConfigWithIdentity path) is returned unchanged.
func normalizeUnresolvedNodeConfig(cfg ExtConfig) ExtConfig {
	if cfg.RuntimeKind != "" || cfg.RuntimeLanguage != "" || cfg.EntrypointKind != "" || cfg.Source == "" {
		return cfg
	}
	definition, err := extsource.Resolve(cfg.Source)
	if err != nil || definition.Language != "node" {
		return cfg
	}
	cfg.RuntimeKind = "subprocess"
	cfg.RuntimeLanguage = "node"
	cfg.EntrypointKind = string(definition.Form)
	return cfg
}

func packGroupKey(cfg ExtConfig) string {
	sdk := cfg.SDKName
	if sdk == "" {
		sdk = "none"
	}
	identity := "default"
	if cfg.RuntimeLanguage == "go" {
		if root := runtimecell.GoSDKPathOverride(cfg.Source); root != "" {
			identity = root
		}
	}
	if cfg.RuntimeLanguage == "rust" {
		if root := runtimecell.RustSDKPathOverride(cfg.Source); root != "" {
			identity = root
		}
	}
	return strings.Join([]string{cfg.RuntimeKind, cfg.RuntimeLanguage, sdk, identity}, ":")
}

func isolatedCell(cfg ExtConfig, reason string) CellSpec {
	return isolatedCellAt(cfg, reason, 0)
}

func isolatedCellAt(cfg ExtConfig, reason string, order int) CellSpec {
	hash := cellHash("isolated", []ExtConfig{cfg})
	return CellSpec{
		Key:        "isolated:" + cfg.Name + ":" + hash[:16],
		Hash:       hash,
		Strategy:   CellStrategyIsolated,
		Runtime:    valueOr(cfg.RuntimeKind, "subprocess"),
		Language:   valueOr(cfg.RuntimeLanguage, "unknown"),
		SDK:        sdkKey(cfg),
		Isolation:  reason,
		Order:      order,
		Extensions: []ExtConfig{cfg},
	}
}

func packedCell(groupKey string, configs []ExtConfig) CellSpec {
	return packedCellAt(groupKey, configs, 0)
}

func packedCellAt(groupKey string, configs []ExtConfig, order int) CellSpec {
	first := configs[0]
	strategy := CellStrategyPackedGo
	prefix := "packed-go"
	if first.RuntimeLanguage == "rust" {
		strategy = CellStrategyPackedRust
		prefix = "packed-rust"
	}
	if first.RuntimeLanguage == "python" {
		strategy = CellStrategyPackedPython
		prefix = "packed-python"
	}
	if first.RuntimeLanguage == "node" {
		strategy = CellStrategyPackedNode
		prefix = "packed-node"
	}
	hash := cellHash(prefix+":"+groupKey, configs)
	return CellSpec{
		Key:        prefix + ":" + hash[:16],
		Hash:       hash,
		Strategy:   strategy,
		Runtime:    first.RuntimeKind,
		Language:   first.RuntimeLanguage,
		SDK:        sdkKey(first),
		Isolation:  "shared-ok",
		Order:      order,
		Extensions: configs,
	}
}

func cellHash(scope string, configs []ExtConfig) string {
	h := sha256.New()
	_, _ = h.Write([]byte(scope))
	_, _ = h.Write([]byte("\x00"))
	for _, cfg := range configs {
		for _, part := range []string{cfg.Name, cfg.Source, cfg.Path, cfg.Package, cfg.Factory, cfg.ContentHash} {
			_, _ = h.Write([]byte(part))
			_, _ = h.Write([]byte("\x00"))
		}
		for _, moduleRoot := range cfg.GoWorkspaceModules {
			_, _ = h.Write([]byte(moduleRoot))
			_, _ = h.Write([]byte("\x00"))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sdkKey(cfg ExtConfig) string {
	if cfg.SDKName == "" {
		return "none"
	}
	return cfg.SDKName
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func (c CellSpec) GoExtensions() ([]runtimecell.GoExtension, error) {
	if c.Strategy != CellStrategyPackedGo {
		return nil, fmt.Errorf("cell %s is %s, not packed-go", c.Key, c.Strategy)
	}
	out := make([]runtimecell.GoExtension, 0, len(c.Extensions))
	for _, cfg := range c.Extensions {
		if cfg.Source == "" || cfg.Package == "" {
			return nil, fmt.Errorf("extension %q missing source/package for packed-go cell", cfg.Name)
		}
		factory := cfg.Factory
		if factory == "" {
			factory = "Extension"
		}
		out = append(out, runtimecell.GoExtension{
			Name:             cfg.Name,
			Root:             cfg.Source,
			ModulePath:       cfg.ModulePath,
			Package:          cfg.Package,
			Factory:          factory,
			Hash:             valueOr(cfg.ContentHash, cfg.Source),
			WorkspaceModules: append([]string(nil), cfg.GoWorkspaceModules...),
		})
	}
	return out, nil
}

func (c CellSpec) RustExtensions() ([]runtimecell.RustExtension, error) {
	if c.Strategy != CellStrategyPackedRust {
		return nil, fmt.Errorf("cell %s is %s, not packed-rust", c.Key, c.Strategy)
	}
	out := make([]runtimecell.RustExtension, 0, len(c.Extensions))
	for _, cfg := range c.Extensions {
		if cfg.Source == "" || cfg.Package == "" {
			return nil, fmt.Errorf("extension %q missing source/package for packed-rust cell", cfg.Name)
		}
		factory := cfg.Factory
		if factory == "" {
			factory = "new_extension"
		}
		out = append(out, runtimecell.RustExtension{
			Name:    cfg.Name,
			Root:    cfg.Source,
			Package: cfg.Package,
			Factory: factory,
			Hash:    valueOr(cfg.ContentHash, cfg.Source),
		})
	}
	return out, nil
}

func (c CellSpec) PythonExtensions() ([]runtimecell.PythonExtension, error) {
	if c.Strategy != CellStrategyPackedPython {
		return nil, fmt.Errorf("cell %s is %s, not packed-python", c.Key, c.Strategy)
	}
	out := make([]runtimecell.PythonExtension, 0, len(c.Extensions))
	for _, cfg := range c.Extensions {
		if cfg.Source == "" || cfg.Package == "" {
			return nil, fmt.Errorf("extension %q missing source/package for packed-python cell", cfg.Name)
		}
		factory := cfg.Factory
		if factory == "" {
			factory = "new_extension"
		}
		out = append(out, runtimecell.PythonExtension{
			Name:    cfg.Name,
			Root:    cfg.Source,
			Package: cfg.Package,
			Factory: factory,
			Hash:    valueOr(cfg.ContentHash, cfg.Source),
		})
	}
	return out, nil
}
