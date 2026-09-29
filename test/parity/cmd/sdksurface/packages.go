package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/MichaelKinsy/PiG/coding"
)

// Kinds of package-export rows. Only the Node runtime can import Pi's npm
// packages, so these rows have a Node runtime cell alone.
const (
	kindExport       = "export"
	kindExportMember = "export member"
)

// Node runtime statuses for package exports, from the runtime probe.
const (
	statusVendored = "Pi's own code"
	statusBridged  = "bridged"
	statusStandIn  = "stand-in"
)

// piPackage is one module specifier an extension imports, and the runtime
// file that serves it ("" when the runtime serves none).
type piPackage struct {
	module     string // specifier suffix shown in the matrix
	name       string // npm package in the compiler-derived inventory
	entrypoint string
	file       string // relative to coding/extension/host/subprocess/runtime-node
}

// piPackages are the specifiers Pi's extension loader serves from its own
// bundle (core/extensions/virtual-modules.ts): pi-coding-agent, pi-tui, the
// pi-ai root (served as its compat entry point), pi-ai/compat and
// pi-ai/providers/all, and pi-agent-core.
var piPackages = []piPackage{
	{"pi-coding-agent", "@earendil-works/pi-coding-agent", ".", "shims/pi-coding-agent.mjs"},
	{"pi-tui", "@earendil-works/pi-tui", ".", "shims/pi-tui.mjs"},
	{"pi-ai", "@earendil-works/pi-ai", ".", "shims/pi-ai.mjs"},
	{"pi-ai/compat", "@earendil-works/pi-ai", "./compat", "shims/pi-ai.mjs"},
	{"pi-ai/providers/all", "@earendil-works/pi-ai", "./providers/all", "shims/pi-dist/pi-ai/providers/all.js"},
	{"pi-agent-core", "@earendil-works/pi-agent-core", ".", "shims/pi-agent-core.mjs"},
}

type inventoryFile struct {
	Interfaces []struct {
		Package    string `json:"package"`
		Entrypoint string `json:"entrypoint"`
		Name       string `json:"name"`
		Kind       string `json:"kind"`
		Shape      struct {
			Properties []struct {
				Name string `json:"name"`
			} `json:"properties"`
		} `json:"shape"`
	} `json:"interfaces"`
}

type probeExport struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Members []string `json:"members,omitempty"`
}

type probePackage struct {
	Module  string        `json:"module"`
	File    string        `json:"file"`
	Exports []probeExport `json:"exports"`
}

type probeCell struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type probeResult struct {
	Symbols  []string `json:"symbols"`
	Packages map[string]map[string]struct {
		probeCell
		Members map[string]probeCell `json:"members"`
	} `json:"packages"`
}

// packageExports reads every runtime export (class, function, variable) of
// Pi's extension-facing packages, and each exported class's public members,
// from the compiler-derived inventory of the pinned packages' .d.ts files.
func packageExports(root string) ([]probePackage, error) {
	raw, err := os.ReadFile(filepath.Join(root, "test/parity/interfaces/upstream-v"+coding.UpstreamVersion+".json"))
	if err != nil {
		return nil, err
	}
	var inv inventoryFile
	if err := json.Unmarshal(raw, &inv); err != nil {
		return nil, err
	}
	var out []probePackage
	for _, p := range piPackages {
		pkg := probePackage{Module: p.module, File: p.file}
		for _, i := range inv.Interfaces {
			if i.Package != p.name || i.Entrypoint != p.entrypoint {
				continue
			}
			switch i.Kind {
			case "class", "function", "variable":
			default:
				continue // type-only declarations have no runtime value
			}
			exp := probeExport{Name: i.Name, Kind: i.Kind}
			if i.Kind == "class" {
				for _, prop := range i.Shape.Properties {
					exp.Members = append(exp.Members, prop.Name)
				}
				sort.Strings(exp.Members)
			}
			pkg.Exports = append(pkg.Exports, exp)
		}
		if len(pkg.Exports) == 0 {
			return nil, fmt.Errorf("inventory has no runtime exports for %s %s", p.name, p.entrypoint)
		}
		sort.Slice(pkg.Exports, func(a, b int) bool { return pkg.Exports[a].Name < pkg.Exports[b].Name })
		out = append(out, pkg)
	}
	return out, nil
}

// runProbe instantiates the Node runtime in node and reports the members of
// the objects an extension reaches and the origin of every package export.
func runProbe(root string, packages []probePackage) (*probeResult, error) {
	dir, err := os.MkdirTemp("", "sdksurface-probe-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	request, err := json.Marshal(map[string]any{"packages": packages})
	if err != nil {
		return nil, err
	}
	requestPath := filepath.Join(dir, "request.json")
	if err := os.WriteFile(requestPath, request, 0o600); err != nil {
		return nil, err
	}
	runtimeDir, err := filepath.Abs(filepath.Join(root, "coding/extension/host/subprocess/runtime-node"))
	if err != nil {
		return nil, err
	}
	probe, err := filepath.Abs(filepath.Join(root, "test/parity/cmd/sdksurface/probe.mjs"))
	if err != nil {
		return nil, err
	}
	outputPath := filepath.Join(dir, "report.json")
	cmd := exec.Command("node", probe, runtimeDir, requestPath, outputPath)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("node runtime probe: %w\n%s", err, output)
	}
	report, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, err
	}
	var result probeResult
	if err := json.Unmarshal(report, &result); err != nil {
		return nil, fmt.Errorf("node runtime probe output: %w", err)
	}
	return &result, nil
}

// packageRows turns the probe's package report into matrix rows.
func packageRows(packages []probePackage, probe *probeResult) []matrixRow {
	nodeCell := func(c probeCell) cell {
		switch c.Status {
		case "vendored":
			return cell{Status: statusVendored}
		case "bridged":
			return cell{Status: statusBridged}
		case "stand-in":
			return cell{Status: statusStandIn, Note: c.Reason}
		}
		return cell{Status: statusMissing}
	}
	var rows []matrixRow
	for _, pkg := range packages {
		report := probe.Packages[pkg.Module]
		group := "Package exports: " + pkg.Module
		for _, exp := range pkg.Exports {
			got := report[exp.Name]
			rows = append(rows, matrixRow{
				surfaceRow: surfaceRow{Group: group, Key: pkg.Module + ": " + exp.Name, Decl: exp.Kind + " " + exp.Name, Kind: kindExport},
				Cells:      map[string]cell{"node": nodeCell(got.probeCell)},
				Exception:  map[string]string{},
			})
			for _, m := range exp.Members {
				rows = append(rows, matrixRow{
					surfaceRow: surfaceRow{Group: group, Key: pkg.Module + ": " + exp.Name + "." + m, Decl: exp.Name + "." + m, Kind: kindExportMember},
					Cells:      map[string]cell{"node": nodeCell(got.Members[m])},
					Exception:  map[string]string{},
				})
			}
		}
	}
	return rows
}

// isPackageRow reports whether a row is a package export, which only the
// Node runtime column applies to.
func isPackageRow(r matrixRow) bool { return r.Kind == kindExport || r.Kind == kindExportMember }

// rowSDKs lists the runtimes whose cells a row has.
func rowSDKs(r matrixRow) []string {
	if isPackageRow(r) {
		return []string{"node"}
	}
	return sdks
}
