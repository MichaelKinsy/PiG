package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/extension/pigsdk"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// extensionValidationReport is the JSON contract emitted by `pig install
// --validate-only --json`.
//
// pig additive (D28): downstream-only extension validation report surface;
// upstream pi has no validate-only install mode or machine-readable extension
// validation output. External controllers can consume it to gate publication.
type extensionValidationReport struct {
	Valid     bool     `json:"valid"`
	Name      string   `json:"name"`
	Source    string   `json:"source,omitempty"`
	Path      string   `json:"path,omitempty"`
	Hash      string   `json:"hash,omitempty"`
	Tools     []string `json:"tools,omitempty"`
	Commands  []string `json:"commands,omitempty"`
	Handlers  []string `json:"handlers,omitempty"`
	Flags     []string `json:"flags,omitempty"`
	Shortcuts []string `json:"shortcuts,omitempty"`
	Providers []string `json:"providers,omitempty"`
	// ToolDetails and CommandDetails carry the model- and user-facing text of
	// each registered surface (descriptions, prompt snippets, guidelines) so a
	// downstream validator can scan it for prompt injection. The flat name
	// lists above stay authoritative for counts and duplicate detection.
	ToolDetails    []toolDetailReport               `json:"toolDetails,omitempty"`
	CommandDetails []commandDetailReport            `json:"commandDetails,omitempty"`
	Registered     bool                             `json:"registered"`
	Definition     *extensionValidationSourceReport `json:"definition,omitempty"`
	Phase          string                           `json:"phase,omitempty"`
	Code           string                           `json:"code,omitempty"`
	StderrLog      string                           `json:"stderrLog,omitempty"`
	Warnings       []string                         `json:"warnings,omitempty"`
	Error          string                           `json:"error,omitempty"`
}

// toolDetailReport is the scannable text surface of one registered tool.
type toolDetailReport struct {
	Name             string   `json:"name"`
	Label            string   `json:"label,omitempty"`
	Description      string   `json:"description,omitempty"`
	PromptSnippet    string   `json:"promptSnippet,omitempty"`
	PromptGuidelines []string `json:"promptGuidelines,omitempty"`
}

// commandDetailReport is the scannable text surface of one registered command.
type commandDetailReport struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type extensionValidationSourceReport struct {
	Form       string `json:"form"`
	Language   string `json:"language"`
	Root       string `json:"root"`
	Entrypoint string `json:"entrypoint,omitempty"`
	Package    string `json:"package,omitempty"`
	Factory    string `json:"factory,omitempty"`
	Packable   bool   `json:"packable"`
	Runtime    string `json:"runtime"`
	SDK        string `json:"sdk,omitempty"`
	Isolation  string `json:"isolation"`
}

type extensionValidationSetReport struct {
	Valid       bool                             `json:"valid"`
	Extensions  []extensionValidationReport      `json:"extensions"`
	Diagnostics []extensionValidationDiagnostic  `json:"diagnostics,omitempty"`
	Placement   extensionValidationPlacementPlan `json:"placement"`
}

type extensionValidationDiagnostic struct {
	Severity   string   `json:"severity"`
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	Kind       string   `json:"kind,omitempty"`
	Name       string   `json:"name,omitempty"`
	Extensions []string `json:"extensions,omitempty"`
}

type extensionValidationPlacementPlan struct {
	Policy string                              `json:"policy"`
	Groups []extensionValidationPlacementGroup `json:"groups,omitempty"`
	Cells  []extensionValidationPlacementCell  `json:"cells,omitempty"`
}

type extensionValidationPlacementGroup struct {
	ID         string   `json:"id"`
	Key        string   `json:"key"`
	Hash       string   `json:"hash,omitempty"`
	Language   string   `json:"language,omitempty"`
	Runtime    string   `json:"runtime,omitempty"`
	SDK        string   `json:"sdk,omitempty"`
	Isolation  string   `json:"isolation,omitempty"`
	Extensions []string `json:"extensions"`
	Strategy   string   `json:"strategy"`
	Status     string   `json:"status"`
}

type extensionValidationPlacementCell struct {
	ID         string   `json:"id"`
	GroupID    string   `json:"groupId,omitempty"`
	Key        string   `json:"key,omitempty"`
	Hash       string   `json:"hash,omitempty"`
	Language   string   `json:"language,omitempty"`
	Runtime    string   `json:"runtime,omitempty"`
	SDK        string   `json:"sdk,omitempty"`
	Isolation  string   `json:"isolation,omitempty"`
	Extensions []string `json:"extensions"`
	Action     string   `json:"action"`
	Status     string   `json:"status"`
}

func validateInstallSources(cwd string, sources []string, jsonOut bool) int {
	sources = compactStrings(sources)
	if len(sources) == 0 {
		return printValidationError("missing install source", jsonOut)
	}
	if err := pigsdk.EnsureSynced(codingagent.ConfigRoot()); err != nil {
		return printValidationError(fmt.Sprintf("stage extension SDKs: %v", err), jsonOut)
	}
	reports := make([]extensionValidationReport, 0, len(sources))
	valid := true
	for _, source := range sources {
		sourceReports, err := validateResolvedInstallSources(cwd, source)
		if err != nil {
			valid = false
			reports = append(reports, validationErrorReport(source, err))
			continue
		}
		reports = append(reports, sourceReports...)
	}
	if len(sources) == 1 && len(reports) == 1 {
		report := reports[0]
		if !report.Valid {
			if jsonOut {
				data, _ := json.MarshalIndent(report, "", "  ")
				_, _ = fmt.Fprintln(os.Stdout, string(data))
			} else {
				fmt.Fprintln(os.Stderr, "error:", report.Error)
			}
			return 1
		}
		if jsonOut {
			data, _ := json.MarshalIndent(report, "", "  ")
			_, _ = fmt.Fprintln(os.Stdout, string(data))
			return 0
		}
		_, _ = fmt.Fprintf(os.Stdout, "valid extension: %s\n", report.Name)
		_, _ = fmt.Fprintf(os.Stdout, "registered: tools=%d commands=%d handlers=%d flags=%d shortcuts=%d\n", len(report.Tools), len(report.Commands), len(report.Handlers), len(report.Flags), len(report.Shortcuts))
		return 0
	}

	diagnostics := linkDiagnostics(reports)
	if len(diagnostics) > 0 {
		valid = false
	}
	setReport := extensionValidationSetReport{
		Valid:       valid,
		Extensions:  reports,
		Diagnostics: diagnostics,
		Placement:   isolatedPlacementPlan(reports),
	}
	if jsonOut {
		data, _ := json.MarshalIndent(setReport, "", "  ")
		_, _ = fmt.Fprintln(os.Stdout, string(data))
	} else {
		for _, report := range reports {
			if report.Valid {
				_, _ = fmt.Fprintf(os.Stdout, "valid extension: %s\n", report.Name)
				continue
			}
			_, _ = fmt.Fprintf(os.Stderr, "error validating %s: %s\n", report.Name, report.Error)
		}
	}
	if !valid {
		return 1
	}
	return 0
}

func validateResolvedInstallSources(cwd, source string) ([]extensionValidationReport, error) {
	root, err := resolveInputPackageSourceRoot(cwd, source)
	if err != nil {
		return nil, err
	}
	return validateExtensionRuntimes(root)
}

func validationErrorReport(name string, err error) extensionValidationReport {
	report := extensionValidationReport{Valid: false, Name: name, Error: err.Error()}
	if loadErr, ok := errors.AsType[*subprocess.LoadError](err); ok {
		report.Name = loadErr.Extension
		report.Phase = loadErr.Phase
		report.Code = loadErr.Code
		report.StderrLog = loadErr.StderrLog
	}
	return report
}

func printValidationError(message string, jsonOut bool) int {
	if jsonOut {
		data, _ := json.MarshalIndent(map[string]any{"valid": false, "error": message}, "", "  ")
		_, _ = fmt.Fprintln(os.Stdout, string(data))
	} else {
		fmt.Fprintln(os.Stderr, "error:", message)
	}
	return 1
}

func validateExtensionRuntimes(input string) ([]extensionValidationReport, error) {
	abs, err := filepath.Abs(input)
	if err != nil {
		return nil, err
	}
	configs, definitionReports, definitions, err := extensionConfigsFromPath(abs)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	host := subprocess.NewHostWithConfigRoot(filepath.Dir(abs), codingagent.ConfigRoot())
	defer host.Shutdown("validation complete")
	loaded, loadErrors := host.LoadAll(ctx, configs)
	if len(loadErrors) > 0 {
		return nil, errors.Join(loadErrors...)
	}
	if len(loaded) != len(configs) || host.ExtensionCount() != len(configs) {
		return nil, fmt.Errorf("registered %d of %d extensions", len(loaded), len(configs))
	}
	byName := make(map[string]*extension.Extension, len(loaded))
	for i := range loaded {
		byName[loaded[i].Name] = &loaded[i]
	}
	reports := make([]extensionValidationReport, 0, len(configs))
	for i, cfg := range configs {
		ext := byName[cfg.Name]
		if ext == nil {
			return nil, fmt.Errorf("extension %q did not register", cfg.Name)
		}
		hash, err := extensionContentHash(abs, definitions[i])
		if err != nil {
			return nil, err
		}
		reports = append(reports, extensionValidationReport{
			Valid: true, Name: cfg.Name, Source: cfg.Source, Path: cfg.Path, Hash: hash,
			Tools: mapKeys(ext.Tools), Commands: mapKeys(ext.Commands), Handlers: mapKeys(ext.Handlers),
			Flags: mapKeys(ext.Flags), Shortcuts: shortcutKeys(ext.Shortcuts), Providers: host.ProviderNames(cfg.Name),
			ToolDetails: toolDetailsFromExtension(ext), CommandDetails: commandDetailsFromExtension(ext),
			Registered: true, Definition: definitionReports[i],
		})
	}
	return reports, nil
}

// toolDetailsFromExtension projects each registered tool's model-facing text
// (label, description, prompt snippet, guidelines) in deterministic name order
// so a downstream validator can scan it for prompt injection.
//
// pig additive (D28): the toolDetails/commandDetails scannable-text surface
// has no upstream equivalent.
func toolDetailsFromExtension(ext *extension.Extension) []toolDetailReport {
	names := mapKeys(ext.Tools)
	out := make([]toolDetailReport, 0, len(names))
	for _, name := range names {
		def := ext.Tools[name].Definition
		out = append(out, toolDetailReport{
			Name:             name,
			Label:            def.Label,
			Description:      def.Description,
			PromptSnippet:    def.PromptSnippet,
			PromptGuidelines: def.PromptGuidelines,
		})
	}
	return out
}

// commandDetailsFromExtension projects each registered command's description in
// deterministic name order for the same downstream injection scan.
func commandDetailsFromExtension(ext *extension.Extension) []commandDetailReport {
	names := mapKeys(ext.Commands)
	out := make([]commandDetailReport, 0, len(names))
	for _, name := range names {
		out = append(out, commandDetailReport{
			Name:        name,
			Description: ext.Commands[name].Description,
		})
	}
	return out
}

func extensionConfigsFromPath(abs string) ([]subprocess.ExtConfig, []*extensionValidationSourceReport, []*extsource.Definition, error) {
	config, definition, err := subprocess.ResolveExtConfig(abs)
	if err != nil {
		return nil, nil, nil, err
	}
	report := &extensionValidationSourceReport{
		Form: string(definition.Form), Language: definition.Language, Root: definition.Root,
		Entrypoint: definition.Entrypoint, Package: definition.Package,
		Factory: definition.Factory, Packable: definition.Packable,
		Runtime: config.RuntimeKind, SDK: config.SDKName, Isolation: config.Isolation,
	}
	return []subprocess.ExtConfig{config}, []*extensionValidationSourceReport{report}, []*extsource.Definition{definition}, nil
}

func extensionContentHash(abs string, definition *extsource.Definition) (string, error) {
	h := sha256.New()
	if definition != nil {
		definitionBytes, _ := json.Marshal(definition)
		h.Write([]byte("definition\x00"))
		h.Write(definitionBytes)
		h.Write([]byte("\x00"))
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		if err := hashFile(h, filepath.Dir(abs), abs); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if shouldSkipHashDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || shouldSkipHashFile(d.Name()) {
			return nil
		}
		return hashFile(h, abs, path)
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashFile(h interface {
	Write([]byte) (int, error)
}, root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	_, _ = h.Write([]byte(rel))
	_, _ = h.Write([]byte("\x00"))
	_, _ = h.Write(data)
	_, _ = h.Write([]byte("\x00"))
	return nil
}

func shouldSkipHashDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "target", "vendor", "dist", "build", "__pycache__":
		return true
	default:
		return false
	}
}

func shouldSkipHashFile(name string) bool {
	return strings.HasSuffix(name, ".tmp") || strings.HasSuffix(name, "~")
}

func linkDiagnostics(reports []extensionValidationReport) []extensionValidationDiagnostic {
	var diagnostics []extensionValidationDiagnostic
	identityOwners := make(map[string][]string)
	for _, report := range reports {
		if report.Valid {
			origin := report.Source
			if origin == "" {
				origin = report.Path
			}
			identityOwners[report.Name] = append(identityOwners[report.Name], origin)
		}
	}
	for name, owners := range identityOwners {
		if len(owners) > 1 {
			diagnostics = append(diagnostics, extensionValidationDiagnostic{
				Severity: "error", Code: "duplicate_extension", Kind: "extension", Name: name,
				Extensions: owners, Message: fmt.Sprintf("duplicate extension identity %q from: %s", name, strings.Join(owners, ", ")),
			})
		}
	}
	check := func(kind string, names func(extensionValidationReport) []string) {
		owners := map[string][]string{}
		for _, report := range reports {
			if !report.Valid {
				continue
			}
			for _, name := range names(report) {
				owners[name] = append(owners[name], report.Name)
			}
		}
		for name, exts := range owners {
			if len(exts) <= 1 {
				continue
			}
			slices.Sort(exts)
			diagnostics = append(diagnostics, extensionValidationDiagnostic{
				Severity:   "error",
				Code:       "duplicate_" + kind,
				Kind:       kind,
				Name:       name,
				Extensions: exts,
				Message:    fmt.Sprintf("duplicate %s %q registered by: %s", kind, name, strings.Join(exts, ", ")),
			})
		}
	}
	check("tool", func(r extensionValidationReport) []string { return r.Tools })
	check("command", func(r extensionValidationReport) []string { return r.Commands })
	check("flag", func(r extensionValidationReport) []string { return r.Flags })
	check("shortcut", func(r extensionValidationReport) []string { return r.Shortcuts })
	check("provider", func(r extensionValidationReport) []string { return r.Providers })
	slices.SortFunc(diagnostics, func(a, b extensionValidationDiagnostic) int {
		if a.Code != b.Code {
			return strings.Compare(a.Code, b.Code)
		}
		return strings.Compare(a.Name, b.Name)
	})
	return diagnostics
}

func isolatedPlacementPlan(reports []extensionValidationReport) extensionValidationPlacementPlan {
	plan := extensionValidationPlacementPlan{Policy: "auto"}
	groupIndex := map[string]int{}
	groupNames := map[string][]string{}
	groupMeta := map[string]extensionValidationPlacementGroup{}
	for _, report := range reports {
		if !report.Valid {
			continue
		}
		meta := placementMeta(report)
		key := meta.key(report.Name)
		if _, ok := groupIndex[key]; !ok {
			groupIndex[key] = len(groupIndex) + 1
			groupMeta[key] = extensionValidationPlacementGroup{
				ID:        fmt.Sprintf("group-%d", groupIndex[key]),
				Key:       key,
				Language:  meta.language,
				Runtime:   meta.runtime,
				SDK:       meta.sdk,
				Isolation: meta.isolation,
				Strategy:  meta.strategy(),
				Status:    "valid",
			}
		}
		groupNames[key] = append(groupNames[key], report.Name)
	}
	keys := make([]string, 0, len(groupIndex))
	for key := range groupIndex {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b string) int { return groupIndex[a] - groupIndex[b] })
	for _, key := range keys {
		group := groupMeta[key]
		slices.Sort(groupNames[key])
		group.Extensions = groupNames[key]
		group.Hash = placementHash("group", key, group.Extensions, reports)
		plan.Groups = append(plan.Groups, group)
	}
	// Build cells: pack-candidate groups share a cell; others are isolated.
	packedCells := map[string]*extensionValidationPlacementCell{}
	isolatedIdx := 0
	for _, report := range reports {
		if !report.Valid {
			continue
		}
		meta := placementMeta(report)
		key := meta.key(report.Name)
		group := groupMeta[key]
		strategy := meta.strategy()

		if strategy == "pack-candidate" {
			if cell, ok := packedCells[key]; ok {
				cell.Extensions = append(cell.Extensions, report.Name)
				continue
			}
			cell := extensionValidationPlacementCell{
				ID:         fmt.Sprintf("packed-%s-%d", meta.language, len(packedCells)+1),
				GroupID:    group.ID,
				Language:   meta.language,
				Runtime:    meta.runtime,
				SDK:        meta.sdk,
				Isolation:  meta.isolation,
				Extensions: []string{report.Name},
				Action:     "pack",
				Status:     "valid",
			}
			packedCells[key] = &cell
		} else {
			isolatedIdx++
			cellHash := placementHash("cell", key, []string{report.Name}, reports)
			plan.Cells = append(plan.Cells, extensionValidationPlacementCell{
				ID:         fmt.Sprintf("%s-isolated-%d", meta.language, isolatedIdx),
				GroupID:    group.ID,
				Key:        "cell:" + cellHash[:16],
				Hash:       cellHash,
				Language:   meta.language,
				Runtime:    meta.runtime,
				SDK:        meta.sdk,
				Isolation:  meta.isolation,
				Extensions: []string{report.Name},
				Action:     "isolate",
				Status:     "valid",
			})
		}
	}
	// Finalize packed cells.
	packedKeys := make([]string, 0, len(packedCells))
	for key := range packedCells {
		packedKeys = append(packedKeys, key)
	}
	slices.Sort(packedKeys)
	for _, key := range packedKeys {
		cell := packedCells[key]
		slices.Sort(cell.Extensions)
		cell.Hash = placementHash("cell", key, cell.Extensions, reports)
		cell.Key = "cell:" + cell.Hash[:16]
		plan.Cells = append(plan.Cells, *cell)
	}
	return plan
}

func placementHash(scope, placementKey string, names []string, reports []extensionValidationReport) string {
	reportByName := make(map[string]extensionValidationReport, len(reports))
	for _, report := range reports {
		reportByName[report.Name] = report
	}
	h := sha256.New()
	h.Write([]byte(scope))
	h.Write([]byte("\x00"))
	h.Write([]byte(placementKey))
	h.Write([]byte("\x00"))
	for _, name := range names {
		report := reportByName[name]
		h.Write([]byte(name))
		h.Write([]byte("\x00"))
		h.Write([]byte(report.Hash))
		h.Write([]byte("\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

type placementMetadata struct {
	language   string
	runtime    string
	sdk        string
	isolation  string
	entrypoint string
}

func (m placementMetadata) key(extensionName string) string {
	parts := []string{m.runtime, m.language, m.sdk, m.entrypoint}
	if m.isolation == "isolated" || m.isolation == "required" || m.isolation == "strict" {
		parts = append(parts, "isolated", extensionName)
	} else {
		parts = append(parts, "shared-ok")
	}
	return strings.Join(parts, ":")
}

func (m placementMetadata) strategy() string {
	if m.isolation == "isolated" || m.isolation == "required" || m.isolation == "strict" {
		return "isolated"
	}
	if m.runtime == "subprocess" && (m.language == "go" || m.language == "rust" || m.language == "python") && m.entrypoint == "factory" {
		return "pack-candidate"
	}
	return "isolated"
}

func placementMeta(report extensionValidationReport) placementMetadata {
	meta := placementMetadata{language: "unknown", runtime: "unknown", sdk: "none", isolation: "isolated", entrypoint: "unknown"}
	if report.Definition == nil {
		return meta
	}
	meta.language = report.Definition.Language
	meta.runtime = report.Definition.Runtime
	meta.isolation = report.Definition.Isolation
	meta.entrypoint = report.Definition.Form
	if report.Definition.SDK != "" {
		meta.sdk = report.Definition.SDK
	}
	return meta
}

func compactStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func splitInstallSetSources(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\t' || r == ' '
	})
	return compactStrings(fields)
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

func shortcutKeys[K ~string, V any](m map[K]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, string(key))
	}
	slices.Sort(out)
	return out
}
