package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/MichaelKinsy/PiG/coding"
)

type inventory struct {
	UpstreamVersion   string             `json:"upstreamVersion"`
	TypeScriptVersion string             `json:"typescriptVersion"`
	Origin            string             `json:"origin"`
	Packages          []inventoryPackage `json:"packages"`
	Interfaces        []inventoryEntry   `json:"interfaces"`
}

type inventoryPackage struct {
	Key         string                `json:"key"`
	Name        string                `json:"name"`
	Version     string                `json:"version"`
	Entrypoints []inventoryEntrypoint `json:"entrypoints"`
}

type inventoryEntrypoint struct {
	Subpath string `json:"subpath"`
	File    string `json:"file"`
}

type inventoryEntry struct {
	ID         string          `json:"id"`
	ParentID   string          `json:"parentId"`
	Role       string          `json:"role"`
	Package    string          `json:"package"`
	Entrypoint string          `json:"entrypoint"`
	Name       string          `json:"name"`
	Kind       string          `json:"kind"`
	Shape      json.RawMessage `json:"shape"`
	ShapeHash  string          `json:"shapeHash"`
	Source     json.RawMessage `json:"source"`
}

type observableInventory struct {
	UpstreamVersion string                `json:"upstreamVersion"`
	Kind            string                `json:"kind"`
	Interfaces      []observableInterface `json:"interfaces"`
}

type observableInterface struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	ShapeHash string          `json:"shapeHash"`
	Command   string          `json:"command"`
	Flag      string          `json:"flag"`
	Aliases   []string        `json:"aliases"`
	Value     string          `json:"value"`
	Parser    sourceLocation  `json:"parser"`
	Help      *sourceLocation `json:"help"`
}

type sourceLocation struct {
	Path string `json:"path"`
	Line int    `json:"line"`
}

type mappingLedger struct {
	UpstreamVersion string         `json:"upstreamVersion"`
	Mappings        []mappingEntry `json:"mappings"`
}

type mappingEntry struct {
	ID                string            `json:"id"`
	Disposition       string            `json:"disposition"`
	UpstreamShapeHash string            `json:"upstreamShapeHash"`
	PigTargets        []string          `json:"pigTargets"`
	Layers            map[string]string `json:"layers"`
	Production        []string          `json:"production"`
	Evidence          []string          `json:"evidence"`
	BehaviorContracts []string          `json:"behaviorContracts"`
	AsyncContract     string            `json:"asyncContract"`
	Divergence        string            `json:"divergence"`
	Rationale         string            `json:"rationale"`
}

type behaviorLedger struct {
	Contracts []struct {
		ID     string `toml:"id"`
		Status string `toml:"status"`
	} `toml:"contract"`
}

var dispositions = map[string]struct{}{
	"ported": {}, "partial": {}, "deferred": {}, "designed-out": {}, "divergence": {}, "pending": {},
}

var layerStatuses = map[string]struct{}{
	"complete": {}, "partial": {}, "pending": {}, "n/a": {},
}

var mappingLayers = map[string]struct{}{
	"shape": {}, "api": {}, "parser": {}, "help": {}, "consumer": {}, "wire": {}, "host-dispatch": {},
	"sdk-go": {}, "sdk-rust": {}, "sdk-python": {}, "isolated-conformance": {}, "packed-conformance": {},
	"production": {}, "behavior": {}, "persistence": {}, "tui": {}, "async": {},
}

func main() {
	inventoryPath := flag.String("inventory", "parity/interfaces/upstream-v"+coding.UpstreamVersion+".json", "generated upstream interface inventory")
	observablePath := flag.String("observable", "", "generated observable interface inventory")
	upstreamRoot := flag.String("upstream-root", ".upstream/current", "pinned upstream source root for observable locations")
	mappingPath := flag.String("mapping", "", "reviewed Pig interface mapping")
	recommendationsPath := flag.String("recommendations", "", "non-authoritative porting recommendations")
	goInventoryPath := flag.String("go-inventory", "parity/interfaces/pig-go.json", "generated Pig Go candidate inventory")
	behaviorContractsPath := flag.String("behavior-contracts", "parity/behavior-contracts.toml", "reviewed behavioral contract ledger")
	generatePending := flag.Bool("generate-pending", false, "write an all-pending mapping ledger to stdout")
	repoRoot := flag.String("repo-root", ".", "repository root for mapping reference validation")
	strict := flag.Bool("strict", false, "reject pending/partial mappings and incomplete closure")
	flag.Parse()

	var upstream inventory
	if err := decodeJSONFile(*inventoryPath, &upstream); err != nil {
		exitf("interface-inventory: %v", err)
	}
	problems := validateInventory(upstream)
	if *observablePath != "" {
		var observable observableInventory
		if err := decodeJSONFile(*observablePath, &observable); err != nil {
			exitf("interface-inventory: %v", err)
		}
		observableProblems, entries := validateObservable(upstream, observable, *upstreamRoot)
		problems = append(problems, observableProblems...)
		upstream.Interfaces = append(upstream.Interfaces, entries...)
	}
	if *generatePending {
		if len(problems) > 0 {
			exitProblems(problems)
		}
		if err := writePendingMapping(upstream); err != nil {
			exitf("interface-inventory: generate pending mapping: %v", err)
		}
		return
	}
	if *recommendationsPath != "" {
		var recommendations recommendationLedger
		if err := decodeJSONFile(*recommendationsPath, &recommendations); err != nil {
			exitf("interface-inventory: %v", err)
		}
		var pig goInventory
		if err := decodeJSONFile(*goInventoryPath, &pig); err != nil {
			exitf("interface-inventory: %v", err)
		}
		problems = append(problems, validateRecommendations(upstream, recommendations, pig)...)
	}
	if *mappingPath != "" {
		var mappings mappingLedger
		if err := decodeJSONFile(*mappingPath, &mappings); err != nil {
			exitf("interface-inventory: %v", err)
		}
		var behaviors behaviorLedger
		if _, err := toml.DecodeFile(*behaviorContractsPath, &behaviors); err != nil {
			exitf("interface-inventory: decode behavior contracts: %v", err)
		}
		problems = append(problems, validateMappings(upstream, mappings, behaviors, *strict, *repoRoot)...)
	} else if *strict {
		problems = append(problems, "strict mapping validation requires -mapping")
	}
	slices.Sort(problems)
	if len(problems) > 0 {
		exitProblems(problems)
	}
	fmt.Printf("interface-inventory: clean (%d semantic interfaces at %s)\n", len(upstream.Interfaces), upstream.UpstreamVersion)
}

func decodeJSONFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode %s: trailing JSON value", path)
		}
		return fmt.Errorf("decode %s trailing data: %w", path, err)
	}
	return nil
}

func validateInventory(upstream inventory) []string {
	var problems []string
	if upstream.UpstreamVersion == "" {
		problems = append(problems, "inventory upstreamVersion is empty")
	}
	if upstream.TypeScriptVersion != "5.9.3" {
		problems = append(problems, fmt.Sprintf("inventory TypeScript version = %q, want 5.9.3", upstream.TypeScriptVersion))
	}
	if upstream.Origin != "published" {
		problems = append(problems, fmt.Sprintf("inventory origin = %q, want published", upstream.Origin))
	}
	expectedPackages := map[string]string{
		"agent":        "@earendil-works/pi-agent-core",
		"ai":           "@earendil-works/pi-ai",
		"coding-agent": "@earendil-works/pi-coding-agent",
		"tui":          "@earendil-works/pi-tui",
	}
	entrypoints := make(map[string]struct{})
	seenPackages := make(map[string]struct{}, len(upstream.Packages))
	for _, pkg := range upstream.Packages {
		wantName, expected := expectedPackages[pkg.Key]
		if !expected || pkg.Name != wantName {
			problems = append(problems, fmt.Sprintf("inventory package %q has unexpected key/name %q", pkg.Key, pkg.Name))
		}
		if pkg.Version != upstream.UpstreamVersion {
			problems = append(problems, fmt.Sprintf("inventory package %s version = %q, want %q", pkg.Key, pkg.Version, upstream.UpstreamVersion))
		}
		if _, duplicate := seenPackages[pkg.Key]; duplicate {
			problems = append(problems, fmt.Sprintf("duplicate inventory package %s", pkg.Key))
		}
		seenPackages[pkg.Key] = struct{}{}
		for _, entrypoint := range pkg.Entrypoints {
			entrypoints[pkg.Name+"\x00"+entrypoint.Subpath] = struct{}{}
		}
	}
	for key := range expectedPackages {
		if _, exists := seenPackages[key]; !exists {
			problems = append(problems, fmt.Sprintf("missing tracked package %s", key))
		}
	}

	seen := make(map[string]inventoryEntry, len(upstream.Interfaces))
	for _, entry := range upstream.Interfaces {
		if !strings.HasPrefix(entry.ID, "pkg:") || !strings.Contains(entry.ID, "#") {
			problems = append(problems, fmt.Sprintf("invalid stable interface ID %q", entry.ID))
		}
		if !validShapeHash(entry.ShapeHash) {
			problems = append(problems, fmt.Sprintf("%s has invalid shape hash %q", entry.ID, entry.ShapeHash))
		} else if actual, err := semanticShapeHash(entry.Shape); err != nil {
			problems = append(problems, fmt.Sprintf("%s has invalid shape JSON: %v", entry.ID, err))
		} else if entry.ShapeHash != actual {
			problems = append(problems, fmt.Sprintf("%s shape hash = %q, computed = %q", entry.ID, entry.ShapeHash, actual))
		}
		if entry.Kind == "" {
			problems = append(problems, fmt.Sprintf("%s has empty kind", entry.ID))
		}
		if _, exists := entrypoints[entry.Package+"\x00"+entry.Entrypoint]; !exists {
			problems = append(problems, fmt.Sprintf("%s references unknown package entrypoint %s %s", entry.ID, entry.Package, entry.Entrypoint))
		}
		if _, duplicate := seen[entry.ID]; duplicate {
			problems = append(problems, fmt.Sprintf("duplicate interface ID %s", entry.ID))
		}
		if (entry.ParentID == "") != (entry.Role == "") {
			problems = append(problems, fmt.Sprintf("%s must set parentId and role together", entry.ID))
		}
		seen[entry.ID] = entry
	}
	validRoles := map[string]struct{}{"property": {}, "call-overload": {}, "construct-overload": {}, "enum-member": {}}
	for _, entry := range upstream.Interfaces {
		if entry.ParentID == "" {
			continue
		}
		parent, exists := seen[entry.ParentID]
		if !exists {
			problems = append(problems, fmt.Sprintf("%s references missing parent %s", entry.ID, entry.ParentID))
			continue
		}
		if _, valid := validRoles[entry.Role]; !valid {
			problems = append(problems, fmt.Sprintf("%s has invalid member role %q", entry.ID, entry.Role))
		}
		if !strings.HasPrefix(entry.ID, entry.ParentID+"::") {
			problems = append(problems, fmt.Sprintf("%s is not nested under parent ID %s", entry.ID, entry.ParentID))
		}
		if entry.Package != parent.Package || entry.Entrypoint != parent.Entrypoint {
			problems = append(problems, fmt.Sprintf("%s package/entrypoint differs from parent %s", entry.ID, entry.ParentID))
		}
	}
	if len(upstream.Interfaces) == 0 {
		problems = append(problems, "inventory has no interfaces")
	}
	return problems
}

func semanticShapeHash(shape json.RawMessage) (string, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, shape); err != nil {
		return "", err
	}
	sum := sha256.Sum256(compact.Bytes())
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateObservable(upstream inventory, observable observableInventory, repoRoot string) ([]string, []inventoryEntry) {
	var problems []string
	if observable.UpstreamVersion != upstream.UpstreamVersion {
		problems = append(problems, fmt.Sprintf("observable upstreamVersion = %q, inventory = %q", observable.UpstreamVersion, upstream.UpstreamVersion))
	}
	if observable.Kind != "cli" {
		problems = append(problems, fmt.Sprintf("observable kind = %q, want cli", observable.Kind))
	}
	seen := make(map[string]struct{}, len(upstream.Interfaces)+len(observable.Interfaces))
	for _, entry := range upstream.Interfaces {
		seen[entry.ID] = struct{}{}
	}
	entries := make([]inventoryEntry, 0, len(observable.Interfaces))
	for _, entry := range observable.Interfaces {
		if entry.ID != "cli:"+entry.Command+"/"+entry.Flag {
			problems = append(problems, fmt.Sprintf("observable CLI ID %q does not match command/flag", entry.ID))
		}
		if !validShapeHash(entry.ShapeHash) {
			problems = append(problems, fmt.Sprintf("%s has invalid observable shape hash %q", entry.ID, entry.ShapeHash))
		}
		if entry.Kind != "cli-flag" {
			problems = append(problems, fmt.Sprintf("%s kind = %q, want cli-flag", entry.ID, entry.Kind))
		}
		if _, duplicate := seen[entry.ID]; duplicate {
			problems = append(problems, fmt.Sprintf("duplicate semantic interface ID %s", entry.ID))
		}
		seen[entry.ID] = struct{}{}
		problems = append(problems, validateSourceLocation(entry.ID, "parser", entry.Parser, repoRoot)...)
		if entry.Help == nil {
			if entry.Flag != "--help" {
				problems = append(problems, fmt.Sprintf("%s has no help declaration", entry.ID))
			}
		} else {
			problems = append(problems, validateSourceLocation(entry.ID, "help", *entry.Help, repoRoot)...)
		}
		aliases := make(map[string]struct{}, len(entry.Aliases))
		for _, alias := range entry.Aliases {
			if !strings.HasPrefix(alias, "-") || strings.HasPrefix(alias, "--") {
				problems = append(problems, fmt.Sprintf("%s has invalid short alias %q", entry.ID, alias))
			}
			if _, duplicate := aliases[alias]; duplicate {
				problems = append(problems, fmt.Sprintf("%s repeats alias %q", entry.ID, alias))
			}
			aliases[alias] = struct{}{}
		}
		entries = append(entries, inventoryEntry{ID: entry.ID, Kind: entry.Kind, ShapeHash: entry.ShapeHash})
	}
	return problems, entries
}

func validateSourceLocation(id, label string, location sourceLocation, repoRoot string) []string {
	if location.Line < 1 {
		return []string{fmt.Sprintf("%s %s location has invalid line %d", id, label, location.Line)}
	}
	fullPath, err := resolveWithinRoot(repoRoot, location.Path)
	if err != nil {
		return []string{fmt.Sprintf("%s %s location %q: %v", id, label, location.Path, err)}
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return []string{fmt.Sprintf("%s %s location %q: %v", id, label, location.Path, err)}
	}
	if info.IsDir() {
		return []string{fmt.Sprintf("%s %s location %q points to a directory", id, label, location.Path)}
	}
	if err := validateRealPathWithinRoot(repoRoot, fullPath); err != nil {
		return []string{fmt.Sprintf("%s %s location %q: %v", id, label, location.Path, err)}
	}
	return nil
}

func validShapeHash(value string) bool {
	encoded, ok := strings.CutPrefix(value, "sha256:")
	if !ok || len(encoded) != 64 {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}

func validateMappings(upstream inventory, ledger mappingLedger, behaviors behaviorLedger, strict bool, repoRoot string) []string {
	var problems []string
	if ledger.UpstreamVersion != upstream.UpstreamVersion {
		problems = append(problems, fmt.Sprintf("mapping upstreamVersion = %q, inventory = %q", ledger.UpstreamVersion, upstream.UpstreamVersion))
	}
	upstreamIDs := make(map[string]inventoryEntry, len(upstream.Interfaces))
	behaviorStatuses := make(map[string]string, len(behaviors.Contracts))
	for _, contract := range behaviors.Contracts {
		if contract.ID == "" || behaviorStatuses[contract.ID] != "" {
			problems = append(problems, fmt.Sprintf("behavior ledger has empty or duplicate contract %q", contract.ID))
			continue
		}
		behaviorStatuses[contract.ID] = contract.Status
	}
	for _, entry := range upstream.Interfaces {
		upstreamIDs[entry.ID] = entry
	}
	mapped := make(map[string]struct{}, len(ledger.Mappings))
	mappedEntries := make(map[string]mappingEntry, len(ledger.Mappings))
	for _, entry := range ledger.Mappings {
		if _, duplicate := mapped[entry.ID]; duplicate {
			problems = append(problems, fmt.Sprintf("duplicate mapping for %s", entry.ID))
		}
		mapped[entry.ID] = struct{}{}
		mappedEntries[entry.ID] = entry
		upstreamEntry, exists := upstreamIDs[entry.ID]
		if !exists {
			problems = append(problems, fmt.Sprintf("mapping %s has no upstream interface", entry.ID))
		} else if entry.UpstreamShapeHash != upstreamEntry.ShapeHash {
			problems = append(problems, fmt.Sprintf("%s mapping shape hash = %q, current upstream = %q", entry.ID, entry.UpstreamShapeHash, upstreamEntry.ShapeHash))
		}
		if _, ok := dispositions[entry.Disposition]; !ok {
			problems = append(problems, fmt.Sprintf("%s has unknown disposition %q", entry.ID, entry.Disposition))
			continue
		}
		for layer, status := range entry.Layers {
			if _, ok := mappingLayers[layer]; !ok {
				problems = append(problems, fmt.Sprintf("%s has unknown closure layer %q", entry.ID, layer))
			}
			if _, ok := layerStatuses[status]; !ok {
				problems = append(problems, fmt.Sprintf("%s layer %s has unknown status %q", entry.ID, layer, status))
			}
		}
		problems = append(problems, validateMappingClosure(entry, upstreamEntry, strict)...)
		problems = append(problems, validateMappingReferences(entry, repoRoot)...)
		problems = append(problems, validateMappingBehaviorContracts(entry, behaviorStatuses)...)
	}
	for id := range upstreamIDs {
		if _, exists := mapped[id]; !exists {
			problems = append(problems, fmt.Sprintf("missing mapping for %s", id))
		}
	}
	problems = append(problems, validateParentClosure(upstream.Interfaces, mappedEntries)...)
	return problems
}

func validateMappingBehaviorContracts(entry mappingEntry, statuses map[string]string) []string {
	var problems []string
	problems = append(problems, validateMappingList(entry.ID, "behavior contracts", entry.BehaviorContracts)...)
	if entry.Layers["behavior"] == "complete" && strings.Contains(entry.ID, "::property:handleInput") && len(entry.BehaviorContracts) == 0 {
		problems = append(problems, fmt.Sprintf("%s marks handleInput behavior complete without a state-transition contract", entry.ID))
	}
	for _, id := range entry.BehaviorContracts {
		status, exists := statuses[id]
		if !exists {
			problems = append(problems, fmt.Sprintf("%s references unknown behavior contract %q", entry.ID, id))
			continue
		}
		if entry.Layers["behavior"] == "complete" && status != "ported" && status != "divergence" {
			problems = append(problems, fmt.Sprintf("%s marks behavior complete while contract %q is %s", entry.ID, id, status))
		}
	}
	return problems
}

func validateParentClosure(upstream []inventoryEntry, mappedEntries map[string]mappingEntry) []string {
	var problems []string
	for _, upstreamEntry := range upstream {
		if upstreamEntry.ParentID == "" {
			continue
		}
		parent, parentExists := mappedEntries[upstreamEntry.ParentID]
		child, childExists := mappedEntries[upstreamEntry.ID]
		if !parentExists || !childExists || parent.Disposition != "ported" {
			continue
		}
		if child.Disposition != "ported" {
			problems = append(problems, fmt.Sprintf("%s is ported while child %s remains %s", parent.ID, child.ID, child.Disposition))
		}
	}
	return problems
}

func validateMappingClosure(entry mappingEntry, upstream inventoryEntry, strict bool) []string {
	var problems []string
	switch entry.Disposition {
	case "ported":
		if len(entry.PigTargets) == 0 {
			problems = append(problems, fmt.Sprintf("%s is ported without Pig targets", entry.ID))
		}
		if len(entry.Production) == 0 {
			problems = append(problems, fmt.Sprintf("%s is ported without production reachability", entry.ID))
		}
		if len(entry.Evidence) == 0 {
			problems = append(problems, fmt.Sprintf("%s is ported without behavioral/conformance evidence", entry.ID))
		}
		if len(entry.Layers) == 0 {
			problems = append(problems, fmt.Sprintf("%s is ported without required layer closure", entry.ID))
		}
		for layer, status := range entry.Layers {
			if status != "complete" && status != "n/a" {
				problems = append(problems, fmt.Sprintf("%s ported layer %s remains %s", entry.ID, layer, status))
			}
		}
		for _, layer := range requiredMappingLayers(upstream) {
			if entry.Layers[layer] != "complete" {
				problems = append(problems, fmt.Sprintf("%s required layer %s is not complete", entry.ID, layer))
			}
		}
		if (bytes.Contains(upstream.Shape, []byte("Promise<")) || bytes.Contains(upstream.Shape, []byte("AbortSignal"))) && strings.TrimSpace(entry.AsyncContract) == "" {
			problems = append(problems, fmt.Sprintf("%s is ported without an async contract", entry.ID))
		}
	case "partial", "pending":
		if strict {
			problems = append(problems, fmt.Sprintf("%s remains %s", entry.ID, entry.Disposition))
		}
	case "deferred", "designed-out":
		if strings.TrimSpace(entry.Rationale) == "" {
			problems = append(problems, fmt.Sprintf("%s %s mapping requires a rationale", entry.ID, entry.Disposition))
		}
	case "divergence":
		if strings.TrimSpace(entry.Rationale) == "" || !strings.HasPrefix(entry.Divergence, "D") {
			problems = append(problems, fmt.Sprintf("%s divergence mapping requires rationale and D<N> ID", entry.ID))
		}
		if len(entry.PigTargets) == 0 || len(entry.Production) == 0 || len(entry.Evidence) == 0 {
			problems = append(problems, fmt.Sprintf("%s divergence mapping requires targets, production reachability, and evidence", entry.ID))
		}
	}
	return problems
}

func requiredMappingLayers(upstream inventoryEntry) []string {
	if strings.HasPrefix(upstream.ID, "cli:") {
		return []string{"parser", "help", "consumer", "behavior"}
	}
	if strings.Contains(upstream.ID, "#ExtensionAPI::property:on::call:") {
		return []string{"shape", "api", "wire", "host-dispatch", "sdk-go", "sdk-rust", "sdk-python", "isolated-conformance", "packed-conformance", "production", "behavior"}
	}
	return []string{"shape", "production", "behavior"}
}

func validateMappingReferences(entry mappingEntry, repoRoot string) []string {
	var problems []string
	problems = append(problems, validateMappingList(entry.ID, "Pig targets", entry.PigTargets)...)
	problems = append(problems, validateMappingList(entry.ID, "production", entry.Production)...)
	problems = append(problems, validateMappingList(entry.ID, "evidence", entry.Evidence)...)
	for _, target := range entry.PigTargets {
		problems = append(problems, validatePathReference(entry.ID, "Pig target", target, repoRoot, nil)...)
	}
	for _, production := range entry.Production {
		problems = append(problems, validatePathReference(entry.ID, "production", production, repoRoot,
			map[string]struct{}{"call": {}, "registry": {}, "wire": {}, "reflection": {}})...)
	}
	for _, evidence := range entry.Evidence {
		problems = append(problems, validatePathReference(entry.ID, "evidence", evidence, repoRoot,
			map[string]struct{}{"test": {}, "scenario": {}, "probe": {}, "conformance": {}})...)
	}
	if entry.Disposition == "divergence" && entry.Divergence != "" {
		problems = append(problems, validateDivergenceReference(entry, repoRoot)...)
	}
	return problems
}

func validateMappingList(id, label string, values []string) []string {
	var problems []string
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, fmt.Sprintf("%s %s contains an empty reference", id, label))
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			problems = append(problems, fmt.Sprintf("%s %s repeats reference %q", id, label, value))
		}
		seen[value] = struct{}{}
	}
	return problems
}

func validateDivergenceReference(entry mappingEntry, repoRoot string) []string {
	content, err := os.ReadFile(filepath.Join(repoRoot, "DIVERGENCES.md"))
	if err != nil {
		return []string{fmt.Sprintf("%s divergence %s: %v", entry.ID, entry.Divergence, err)}
	}
	heading := "## " + entry.Divergence + " "
	start := bytes.Index(content, []byte(heading))
	if start < 0 {
		return []string{fmt.Sprintf("%s references missing divergence %s", entry.ID, entry.Divergence)}
	}
	block := content[start:]
	if next := bytes.Index(block[len(heading):], []byte("\n## D")); next >= 0 {
		block = block[:len(heading)+next]
	}
	if !bytes.Contains(block, []byte("SCRUTINIZED:approved")) {
		return []string{fmt.Sprintf("%s divergence %s is not SCRUTINIZED:approved", entry.ID, entry.Divergence)}
	}
	marker := []byte("pig divergence (" + entry.Divergence + ")")
	for _, target := range entry.PigTargets {
		pathRef, _, _ := strings.Cut(target, "#")
		data, readErr := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(pathRef)))
		if readErr == nil && bytes.Contains(data, marker) {
			return nil
		}
	}
	return []string{fmt.Sprintf("%s divergence %s has no call-site marker in mapped Pig targets", entry.ID, entry.Divergence)}
}

func validatePathReference(id, label, reference, repoRoot string, kinds map[string]struct{}) []string {
	kind := ""
	pathRef := reference
	if kinds != nil {
		var ok bool
		kind, pathRef, ok = strings.Cut(reference, ":")
		if !ok {
			return []string{fmt.Sprintf("%s %s reference %q has no proof kind", id, label, reference)}
		}
		if _, allowed := kinds[kind]; !allowed {
			return []string{fmt.Sprintf("%s %s reference %q has unknown proof kind %q", id, label, reference, kind)}
		}
	}
	pathRef, fragment, hasFragment := strings.Cut(pathRef, "#")
	if strings.TrimSpace(pathRef) == "" {
		return []string{fmt.Sprintf("%s %s reference %q has no path", id, label, reference)}
	}
	if !hasFragment || strings.TrimSpace(fragment) == "" {
		return []string{fmt.Sprintf("%s %s reference %q has no concrete fragment", id, label, reference)}
	}
	if err := validateProofKindPath(kind, pathRef); err != nil {
		return []string{fmt.Sprintf("%s %s reference %q: %v", id, label, reference, err)}
	}
	fullPath, err := resolveWithinRoot(repoRoot, pathRef)
	if err != nil {
		return []string{fmt.Sprintf("%s %s reference %q: %v", id, label, reference, err)}
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return []string{fmt.Sprintf("%s %s reference %q: %v", id, label, reference, err)}
	}
	if info.IsDir() {
		return []string{fmt.Sprintf("%s %s reference %q points to a directory", id, label, reference)}
	}
	if err := validateRealPathWithinRoot(repoRoot, fullPath); err != nil {
		return []string{fmt.Sprintf("%s %s reference %q: %v", id, label, reference, err)}
	}
	if err := validateReferenceFragment(fullPath, fragment); err != nil {
		return []string{fmt.Sprintf("%s %s reference %q: %v", id, label, reference, err)}
	}
	return nil
}

func validateProofKindPath(kind, path string) error {
	extension := filepath.Ext(path)
	switch kind {
	case "scenario":
		if extension != ".toml" {
			return fmt.Errorf("scenario proof must reference a TOML scenario")
		}
	case "test", "conformance", "call", "registry", "reflection":
		if extension != ".go" {
			return fmt.Errorf("%s proof must reference a Go file", kind)
		}
	case "wire":
		if !slices.Contains([]string{".go", ".json", ".yaml", ".yml", ".toml"}, extension) {
			return fmt.Errorf("wire proof has unsupported file type %s", extension)
		}
	}
	return nil
}

func resolveWithinRoot(root, reference string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(filepath.Join(rootAbs, filepath.FromSlash(reference)))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes repository root")
	}
	return targetAbs, nil
}

func validateRealPathWithinRoot(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return err
	}
	targetReal, err := filepath.EvalSymlinks(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(rootReal, targetReal)
	if err != nil {
		return err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("symlink escapes repository root")
	}
	return nil
}

func validateReferenceFragment(path, fragment string) error {
	if filepath.Ext(path) == ".go" {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		owner, name, hasOwner := strings.Cut(fragment, ".")
		if !hasOwner {
			name = owner
			owner = ""
		}
		found := false
		ast.Inspect(file, func(node ast.Node) bool {
			switch declaration := node.(type) {
			case *ast.FuncDecl:
				if hasOwner {
					found = found || declaration.Name.Name == name && receiverName(declaration.Recv) == owner
				} else {
					found = found || declaration.Recv == nil && declaration.Name.Name == name
				}
			case *ast.TypeSpec:
				if !hasOwner {
					found = found || declaration.Name.Name == name
				} else if declaration.Name.Name == owner {
					found = found || typeMemberExists(declaration.Type, name)
				}
			case *ast.ValueSpec:
				if !hasOwner {
					for _, candidate := range declaration.Names {
						found = found || candidate.Name == name
					}
				}
			}
			return !found
		})
		if !found {
			return fmt.Errorf("fragment %q does not name a Go declaration", fragment)
		}
		return nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if filepath.Ext(path) == ".toml" {
		for line := range strings.SplitSeq(string(content), "\n") {
			line = strings.TrimSpace(line)
			if line == `name = "`+fragment+`"` {
				return nil
			}
		}
		return fmt.Errorf("fragment %q does not name a TOML scenario", fragment)
	}
	if !bytes.Contains(content, []byte(fragment)) {
		return fmt.Errorf("fragment %q is not present in file", fragment)
	}
	return nil
}

func receiverName(receivers *ast.FieldList) string {
	if receivers == nil || len(receivers.List) == 0 {
		return ""
	}
	typeExpression := receivers.List[0].Type
	if star, ok := typeExpression.(*ast.StarExpr); ok {
		typeExpression = star.X
	}
	identifier, _ := typeExpression.(*ast.Ident)
	if identifier == nil {
		return ""
	}
	return identifier.Name
}

func typeMemberExists(expression ast.Expr, name string) bool {
	var fields *ast.FieldList
	switch value := expression.(type) {
	case *ast.StructType:
		fields = value.Fields
	case *ast.InterfaceType:
		fields = value.Methods
	}
	if fields == nil {
		return false
	}
	for _, field := range fields.List {
		for _, candidate := range field.Names {
			if candidate.Name == name {
				return true
			}
		}
	}
	return false
}

func writePendingMapping(upstream inventory) error {
	type pendingEntry struct {
		ID                string `json:"id"`
		Disposition       string `json:"disposition"`
		UpstreamShapeHash string `json:"upstreamShapeHash"`
	}
	type pendingLedger struct {
		UpstreamVersion string         `json:"upstreamVersion"`
		Mappings        []pendingEntry `json:"mappings"`
	}
	ledger := pendingLedger{UpstreamVersion: upstream.UpstreamVersion, Mappings: make([]pendingEntry, 0, len(upstream.Interfaces))}
	for _, entry := range upstream.Interfaces {
		ledger.Mappings = append(ledger.Mappings, pendingEntry{ID: entry.ID, Disposition: "pending", UpstreamShapeHash: entry.ShapeHash})
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(ledger)
}

func exitProblems(problems []string) {
	slices.Sort(problems)
	const limit = 100
	for _, problem := range problems[:min(len(problems), limit)] {
		fmt.Fprintln(os.Stderr, "interface-inventory:", problem)
	}
	if len(problems) > limit {
		fmt.Fprintf(os.Stderr, "interface-inventory: ... %d additional problem(s) omitted\n", len(problems)-limit)
	}
	os.Exit(1)
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
