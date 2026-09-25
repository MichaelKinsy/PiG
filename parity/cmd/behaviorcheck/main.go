package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/MichaelKinsy/PiG/coding"
)

type ledger struct {
	UpstreamVersion string     `toml:"upstream_version"`
	Contracts       []contract `toml:"contract"`
}

type contract struct {
	ID         string        `toml:"id"`
	UpstreamID string        `toml:"upstream_id"`
	Family     string        `toml:"family"`
	Kind       string        `toml:"kind"`
	Claim      string        `toml:"claim"`
	Status     string        `toml:"status"`
	PigTargets []string      `toml:"pig_targets"`
	Evidence   []string      `toml:"evidence"`
	Upstream   upstreamRange `toml:"upstream"`
}

type inputInventory struct {
	UpstreamVersion string         `json:"upstreamVersion"`
	Keybindings     []keybinding   `json:"keybindings"`
	Handlers        []inputHandler `json:"handlers"`
	Renderers       []renderer     `json:"renderers"`
}

type renderer struct {
	ID              string               `json:"id"`
	Path            string               `json:"path"`
	Owner           string               `json:"owner"`
	Method          string               `json:"method"`
	StartLine       int                  `json:"startLine"`
	EndLine         int                  `json:"endLine"`
	SourceHash      string               `json:"sourceHash"`
	BranchCount     int                  `json:"branchCount"`
	ThemeCalls      []string             `json:"themeCalls"`
	LayoutCalls     []string             `json:"layoutCalls"`
	Glyphs          []string             `json:"glyphs"`
	NumericLiterals []string             `json:"numericLiterals"`
	Dependencies    []rendererDependency `json:"dependencies"`
}

type rendererDependency struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Line  int    `json:"line"`
}

type keybinding struct {
	ID          string              `json:"id"`
	Defaults    map[string][]string `json:"defaults"`
	Description string              `json:"description"`
	Path        string              `json:"path"`
	Line        int                 `json:"line"`
	Consumers   []string            `json:"consumers"`
}

type inputHandler struct {
	ID                string   `json:"id"`
	Path              string   `json:"path"`
	Owner             string   `json:"owner"`
	Method            string   `json:"method"`
	StartLine         int      `json:"startLine"`
	EndLine           int      `json:"endLine"`
	SourceHash        string   `json:"sourceHash"`
	Async             bool     `json:"async"`
	BranchCount       int      `json:"branchCount"`
	Bindings          []string `json:"bindings"`
	RawInputs         []string `json:"rawInputs"`
	Callbacks         []string `json:"callbacks"`
	Delegates         []string `json:"delegates"`
	Mutations         []string `json:"mutations"`
	BoundaryOperators []string `json:"boundaryOperators"`
}

type inputMapping struct {
	UpstreamVersion string              `json:"upstreamVersion"`
	Mappings        []inputMappingEntry `json:"mappings"`
	RenderMappings  []inputMappingEntry `json:"renderMappings"`
}

type inputMappingEntry struct {
	ID          string   `json:"id"`
	OwnerFamily string   `json:"ownerFamily"`
	Disposition string   `json:"disposition"`
	PigTargets  []string `json:"pigTargets"`
	Evidence    []string `json:"evidence"`
	Contracts   []string `json:"contracts"`
	Divergence  string   `json:"divergence"`
	Rationale   string   `json:"rationale"`
}

type familiesConfig struct {
	Families map[string]familyDefinition `toml:"families"`
}

type familyDefinition struct {
	Includes []string `toml:"includes"`
}

type upstreamRange struct {
	Path        string   `toml:"path"`
	Start       string   `toml:"start"`
	End         string   `toml:"end"`
	SHA256      string   `toml:"sha256"`
	Keybindings []string `toml:"keybindings"`
}

func main() {
	path := flag.String("ledger", "parity/behavior-contracts.toml", "behavior contract ledger")
	upstream := flag.String("upstream", ".upstream/current", "pinned upstream root")
	inventoryPath := flag.String("input-inventory", "parity/interfaces/behavior-inputs-v"+coding.UpstreamVersion+".json", "generated upstream input-handler inventory")
	mappingPath := flag.String("input-mapping", "parity/interfaces/behavior-input-mapping-v"+coding.UpstreamVersion+".json", "reviewed input-handler mapping")
	familiesPath := flag.String("families", "parity/families.toml", "behavior family definitions")
	generateMapping := flag.Bool("generate-input-mapping", false, "generate an all-pending input/render mapping")
	previousMappingPath := flag.String("previous-input-mapping", "", "previous reviewed input/render mapping")
	ownerOverridesPath := flag.String("owner-overrides", "parity/interfaces/behavior-owner-overrides-v"+coding.UpstreamVersion+".json", "reviewed owner overrides for ambiguous new surfaces")
	outputMappingPath := flag.String("output-input-mapping", "", "output path for generated input/render mapping")
	strict := flag.Bool("strict", false, "reject pending and partial input-handler mappings")
	family := flag.String("family", "", "report input-handler frontier for one owner family")
	flag.Parse()
	if *generateMapping {
		if err := generatePendingInputMappingFile(*inventoryPath, *previousMappingPath, *familiesPath, *ownerOverridesPath, *outputMappingPath); err != nil {
			fmt.Fprintln(os.Stderr, "behavior mapping:", err)
			os.Exit(1)
		}
		fmt.Printf("behavior mapping: wrote %s\n", *outputMappingPath)
		return
	}
	if err := check(*path, *upstream, *inventoryPath, *mappingPath, *familiesPath, *family, *strict); err != nil {
		fmt.Fprintln(os.Stderr, "behavior contracts:", err)
		os.Exit(1)
	}
}

func check(path, upstreamRoot, inventoryPath, mappingPath, familiesPath, familyName string, strict bool) error {
	var data ledger
	metadata, err := toml.DecodeFile(path, &data)
	if err != nil {
		return err
	}
	if unknown := metadata.Undecoded(); len(unknown) > 0 {
		return fmt.Errorf("behavior ledger has unknown fields: %v", unknown)
	}
	if data.UpstreamVersion != coding.UpstreamVersion {
		return fmt.Errorf("upstream_version = %q, want %q", data.UpstreamVersion, coding.UpstreamVersion)
	}
	var inventory inputInventory
	if err := decodeJSONFile(inventoryPath, &inventory); err != nil {
		return err
	}
	var mapping inputMapping
	if err := decodeJSONFile(mappingPath, &mapping); err != nil {
		return err
	}
	var families familiesConfig
	if _, err := toml.DecodeFile(familiesPath, &families); err != nil {
		return err
	}
	if familyName != "" {
		if _, exists := families.Families[familyName]; !exists {
			return fmt.Errorf("unknown behavior family %q", familyName)
		}
	}
	if inventory.UpstreamVersion != data.UpstreamVersion || mapping.UpstreamVersion != data.UpstreamVersion {
		return fmt.Errorf("behavior ledgers do not agree on upstream version %q", data.UpstreamVersion)
	}
	handlers := make(map[string]inputHandler, len(inventory.Handlers))
	previousHandlerID := ""
	for _, handler := range inventory.Handlers {
		if handler.ID == "" || handlers[handler.ID].ID != "" {
			return fmt.Errorf("input inventory has empty or duplicate id %q", handler.ID)
		}
		if previousHandlerID != "" && handler.ID < previousHandlerID {
			return fmt.Errorf("input inventory is not sorted at %q", handler.ID)
		}
		previousHandlerID = handler.ID
		if handler.Path == "" || handler.Owner == "" || handler.Method == "" || handler.StartLine < 1 || handler.EndLine < handler.StartLine || handler.BranchCount < 0 || !validHash(handler.SourceHash) {
			return fmt.Errorf("input inventory handler %q has invalid identity, location, or source hash", handler.ID)
		}
		for label, values := range map[string][]string{"bindings": handler.Bindings, "rawInputs": handler.RawInputs, "callbacks": handler.Callbacks, "delegates": handler.Delegates, "mutations": handler.Mutations, "boundaryOperators": handler.BoundaryOperators} {
			if !sortedUnique(values) {
				return fmt.Errorf("input inventory handler %q %s are not sorted and unique", handler.ID, label)
			}
		}
		handlers[handler.ID] = handler
	}
	renderers := make(map[string]renderer, len(inventory.Renderers))
	previousRendererID := ""
	for _, item := range inventory.Renderers {
		if item.ID == "" || renderers[item.ID].ID != "" || previousRendererID != "" && item.ID < previousRendererID {
			return fmt.Errorf("renderer inventory has empty, duplicate, or unsorted id %q", item.ID)
		}
		previousRendererID = item.ID
		if item.Path == "" || item.Owner == "" || item.Method == "" || item.StartLine < 1 || item.EndLine < item.StartLine || item.BranchCount < 0 || !validHash(item.SourceHash) {
			return fmt.Errorf("renderer inventory %q has invalid identity, location, or source hash", item.ID)
		}
		for label, values := range map[string][]string{"themeCalls": item.ThemeCalls, "layoutCalls": item.LayoutCalls, "glyphs": item.Glyphs, "numericLiterals": item.NumericLiterals} {
			if !sortedUnique(values) {
				return fmt.Errorf("renderer inventory %q %s are not sorted and unique", item.ID, label)
			}
		}
		previousDependency := ""
		for _, dependency := range item.Dependencies {
			if dependency.Name == "" || dependency.Name <= previousDependency || dependency.Value == "" || dependency.Line < 1 {
				return fmt.Errorf("renderer inventory %q has invalid or unsorted dependency %q", item.ID, dependency.Name)
			}
			previousDependency = dependency.Name
		}
		renderers[item.ID] = item
	}
	previousKeybindingID := ""
	keybindings := make(map[string]bool, len(inventory.Keybindings))
	for _, binding := range inventory.Keybindings {
		if binding.ID == "" || binding.ID <= previousKeybindingID || binding.Path == "" || binding.Line < 1 || binding.Description == "" {
			return fmt.Errorf("keybinding inventory entry %q has invalid or unsorted identity", binding.ID)
		}
		previousKeybindingID = binding.ID
		keybindings[binding.ID] = true
		if len(binding.Defaults) != 4 {
			return fmt.Errorf("keybinding %q must define darwin, linux, linuxWsl, and win32 defaults", binding.ID)
		}
		for _, platform := range []string{"darwin", "linux", "linuxWsl", "win32"} {
			if _, exists := binding.Defaults[platform]; !exists || !uniqueAllowEmpty(binding.Defaults[platform]) {
				return fmt.Errorf("keybinding %q has invalid %s defaults", binding.ID, platform)
			}
		}
		if len(binding.Consumers) == 0 || !sortedUnique(binding.Consumers) {
			return fmt.Errorf("keybinding %q has no sorted consumer inventory", binding.ID)
		}
		for _, consumer := range binding.Consumers {
			if _, exists := handlers[consumer]; !exists {
				return fmt.Errorf("keybinding %q references unknown consumer %q", binding.ID, consumer)
			}
		}
	}
	for _, handler := range inventory.Handlers {
		for _, binding := range handler.Bindings {
			if strings.HasPrefix(binding, "matchesKey:") {
				continue
			}
			if !keybindings[binding] {
				return fmt.Errorf("input handler %q consumes undefined keybinding %q", handler.ID, binding)
			}
		}
	}
	seen := map[string]bool{}
	contracts := make(map[string]contract, len(data.Contracts))
	repoRoot := findRepoRoot(path)
	for i, item := range data.Contracts {
		if item.ID == "" || seen[item.ID] {
			return fmt.Errorf("contract[%d] has empty or duplicate id %q", i, item.ID)
		}
		seen[item.ID] = true
		contracts[item.ID] = item
		if item.Family == "" || item.Kind == "" || item.Claim == "" {
			return fmt.Errorf("contract %q is missing family, kind, or claim", item.ID)
		}
		if item.Status != "ported" && item.Status != "pending" && item.Status != "divergence" {
			return fmt.Errorf("contract %q has unsupported status %q", item.ID, item.Status)
		}
		handler, inputExists := handlers[item.UpstreamID]
		renderer, renderExists := renderers[item.UpstreamID]
		if item.UpstreamID == "" || !inputExists && !renderExists {
			return fmt.Errorf("contract %q references unknown upstream behavior surface %q", item.ID, item.UpstreamID)
		}
		upstreamPath := handler.Path
		if renderExists {
			upstreamPath = renderer.Path
		}
		if item.Upstream.Path != upstreamPath {
			return fmt.Errorf("contract %q upstream path %q does not match behavior surface path %q", item.ID, item.Upstream.Path, upstreamPath)
		}
		segment, err := sourceSegment(upstreamRoot, item.Upstream)
		if err != nil {
			return fmt.Errorf("contract %q: %w", item.ID, err)
		}
		gotHash := fmt.Sprintf("%x", sha256.Sum256([]byte(segment)))
		if gotHash != item.Upstream.SHA256 {
			return fmt.Errorf("contract %q upstream source drift: sha256=%s, want %s", item.ID, gotHash, item.Upstream.SHA256)
		}
		if inputExists && !stringSetsEqual(item.Upstream.Keybindings, handler.Bindings) {
			return fmt.Errorf("contract %q keybindings %v do not match generated input handler bindings %v", item.ID, item.Upstream.Keybindings, handler.Bindings)
		}
		if item.Status == "ported" {
			if len(item.PigTargets) == 0 || len(item.Evidence) < 2 {
				return fmt.Errorf("ported contract %q needs Pig targets plus unit and behavioral evidence", item.ID)
			}
			if err := checkReferences(repoRoot, item.ID, append(append([]string{}, item.PigTargets...), item.Evidence...)); err != nil {
				return err
			}
			if !containsScenario(item.Evidence) {
				return fmt.Errorf("ported contract %q has no parity scenario evidence", item.ID)
			}
		}
	}
	dispositions, err := checkInputMappings(mapping, handlers, contracts, families, repoRoot, strict)
	if err != nil {
		return err
	}
	renderDispositions, err := checkRenderMappings(mapping.RenderMappings, renderers, contracts, families, repoRoot, strict)
	if err != nil {
		return err
	}
	if familyName != "" {
		reportInputFamily(familyName, mapping, handlers, renderers)
	}
	fmt.Printf("behavior contracts: OK (%d contracts, %d input handlers: %d ported, %d partial, %d pending, %d deferred, %d divergence, %d designed-out; upstream %s)\n",
		len(data.Contracts), len(handlers), dispositions["ported"], dispositions["partial"], dispositions["pending"], dispositions["deferred"], dispositions["divergence"], dispositions["designed-out"], data.UpstreamVersion)
	fmt.Printf("render contracts: %d surfaces: %d ported, %d partial, %d pending, %d deferred, %d divergence, %d designed-out\n",
		len(renderers), renderDispositions["ported"], renderDispositions["partial"], renderDispositions["pending"], renderDispositions["deferred"], renderDispositions["divergence"], renderDispositions["designed-out"])
	return nil
}

func reportInputFamily(familyName string, mapping inputMapping, handlers map[string]inputHandler, renderers map[string]renderer) {
	fmt.Printf("Input-handler frontier: %s\n", familyName)
	for _, entry := range mapping.Mappings {
		if entry.OwnerFamily != familyName {
			continue
		}
		handler := handlers[entry.ID]
		fmt.Printf("  %-10s %s (branches=%d bindings=%d mutations=%d callbacks=%d)\n", entry.Disposition, entry.ID, handler.BranchCount, len(handler.Bindings), len(handler.Mutations), len(handler.Callbacks))
	}
	fmt.Printf("Render frontier: %s\n", familyName)
	for _, entry := range mapping.RenderMappings {
		if entry.OwnerFamily != familyName {
			continue
		}
		item := renderers[entry.ID]
		fmt.Printf("  %-10s %s (branches=%d theme=%d layout=%d glyphs=%d constants=%d)\n", entry.Disposition, entry.ID, item.BranchCount, len(item.ThemeCalls), len(item.LayoutCalls), len(item.Glyphs), len(item.Dependencies))
	}
}

func decodeJSONFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("decode %s: trailing JSON data", path)
	}
	return nil
}

func checkInputMappings(mapping inputMapping, handlers map[string]inputHandler, contracts map[string]contract, families familiesConfig, repoRoot string, strict bool) (map[string]int, error) {
	dispositions := map[string]int{}
	mapped := make(map[string]bool, len(mapping.Mappings))
	previousID := ""
	for _, entry := range mapping.Mappings {
		if mapped[entry.ID] {
			return nil, fmt.Errorf("input mapping has duplicate id %q", entry.ID)
		}
		mapped[entry.ID] = true
		if previousID != "" && entry.ID < previousID {
			return nil, fmt.Errorf("input mappings are not sorted at %q", entry.ID)
		}
		previousID = entry.ID
		handler, exists := handlers[entry.ID]
		if !exists {
			return nil, fmt.Errorf("input mapping %q has no generated upstream handler", entry.ID)
		}
		family, exists := families.Families[entry.OwnerFamily]
		if entry.OwnerFamily == "" || !exists {
			return nil, fmt.Errorf("input mapping %q has unknown owner family %q", entry.ID, entry.OwnerFamily)
		}
		if !matchesFamily(handler.Path, family.Includes) {
			return nil, fmt.Errorf("input mapping %q path %q is outside owner family %q", entry.ID, handler.Path, entry.OwnerFamily)
		}
		switch entry.Disposition {
		case "ported", "partial", "pending", "deferred", "divergence", "designed-out":
		default:
			return nil, fmt.Errorf("input mapping %q has unsupported disposition %q", entry.ID, entry.Disposition)
		}
		dispositions[entry.Disposition]++
		for label, values := range map[string][]string{"Pig targets": entry.PigTargets, "evidence": entry.Evidence, "contracts": entry.Contracts} {
			if !unique(values) {
				return nil, fmt.Errorf("input mapping %q has duplicate %s", entry.ID, label)
			}
		}
		if strict && (entry.Disposition == "pending" || entry.Disposition == "partial") {
			return nil, fmt.Errorf("input mapping %q remains %s", entry.ID, entry.Disposition)
		}
		if entry.Disposition == "ported" && (len(entry.PigTargets) == 0 || len(entry.Evidence) == 0 || len(entry.Contracts) == 0) {
			return nil, fmt.Errorf("ported input mapping %q needs Pig targets, evidence, and contracts", entry.ID)
		}
		if entry.Disposition == "partial" && len(entry.Contracts) == 0 {
			return nil, fmt.Errorf("partial input mapping %q needs a behavioral contract", entry.ID)
		}
		if (entry.Disposition == "deferred" || entry.Disposition == "designed-out") && entry.Rationale == "" {
			return nil, fmt.Errorf("%s input mapping %q needs a rationale", entry.Disposition, entry.ID)
		}
		if entry.Disposition == "divergence" && (entry.Divergence == "" || len(entry.Contracts) == 0) {
			return nil, fmt.Errorf("divergence input mapping %q needs a divergence ID and contract", entry.ID)
		}
		if err := checkReferences(repoRoot, entry.ID, append(append([]string{}, entry.PigTargets...), entry.Evidence...)); err != nil {
			return nil, err
		}
		for _, contractID := range entry.Contracts {
			item, exists := contracts[contractID]
			if !exists {
				return nil, fmt.Errorf("input mapping %q references unknown contract %q", entry.ID, contractID)
			}
			if item.UpstreamID != entry.ID {
				return nil, fmt.Errorf("input mapping %q references contract %q for %q", entry.ID, contractID, item.UpstreamID)
			}
			if entry.Disposition == "ported" && item.Status != "ported" && item.Status != "divergence" {
				return nil, fmt.Errorf("ported input mapping %q references %s contract %q", entry.ID, item.Status, contractID)
			}
		}
	}
	for id := range handlers {
		if !mapped[id] {
			return nil, fmt.Errorf("generated input handler %q has no reviewed mapping", id)
		}
	}
	return dispositions, nil
}

func checkRenderMappings(mappings []inputMappingEntry, renderers map[string]renderer, contracts map[string]contract, families familiesConfig, repoRoot string, strict bool) (map[string]int, error) {
	dispositions := map[string]int{}
	mapped := make(map[string]bool, len(mappings))
	previousID := ""
	for _, entry := range mappings {
		if mapped[entry.ID] || previousID != "" && entry.ID < previousID {
			return nil, fmt.Errorf("render mappings have duplicate or unsorted id %q", entry.ID)
		}
		mapped[entry.ID] = true
		previousID = entry.ID
		item, exists := renderers[entry.ID]
		if !exists {
			return nil, fmt.Errorf("render mapping %q has no generated upstream renderer", entry.ID)
		}
		family, exists := families.Families[entry.OwnerFamily]
		if entry.OwnerFamily == "" || !exists || !matchesFamily(item.Path, family.Includes) {
			return nil, fmt.Errorf("render mapping %q path %q has invalid owner family %q", entry.ID, item.Path, entry.OwnerFamily)
		}
		switch entry.Disposition {
		case "ported", "partial", "pending", "deferred", "divergence", "designed-out":
		default:
			return nil, fmt.Errorf("render mapping %q has unsupported disposition %q", entry.ID, entry.Disposition)
		}
		dispositions[entry.Disposition]++
		if strict && (entry.Disposition == "pending" || entry.Disposition == "partial") {
			return nil, fmt.Errorf("render mapping %q remains %s", entry.ID, entry.Disposition)
		}
		for label, values := range map[string][]string{"Pig targets": entry.PigTargets, "evidence": entry.Evidence, "contracts": entry.Contracts} {
			if !unique(values) {
				return nil, fmt.Errorf("render mapping %q has duplicate %s", entry.ID, label)
			}
		}
		if (entry.Disposition == "ported" || entry.Disposition == "partial") && (len(entry.PigTargets) == 0 || len(entry.Evidence) == 0 || len(entry.Contracts) == 0) {
			return nil, fmt.Errorf("%s render mapping %q needs Pig targets, evidence, and contracts", entry.Disposition, entry.ID)
		}
		if (entry.Disposition == "deferred" || entry.Disposition == "designed-out") && entry.Rationale == "" {
			return nil, fmt.Errorf("%s render mapping %q needs a rationale", entry.Disposition, entry.ID)
		}
		if entry.Disposition == "divergence" && (entry.Divergence == "" || len(entry.Contracts) == 0) {
			return nil, fmt.Errorf("divergence render mapping %q needs a divergence ID and contract", entry.ID)
		}
		if err := checkReferences(repoRoot, entry.ID, append(append([]string{}, entry.PigTargets...), entry.Evidence...)); err != nil {
			return nil, err
		}
		for _, contractID := range entry.Contracts {
			contract, exists := contracts[contractID]
			if !exists || contract.UpstreamID != entry.ID {
				return nil, fmt.Errorf("render mapping %q references invalid contract %q", entry.ID, contractID)
			}
			if entry.Disposition == "ported" && contract.Status != "ported" && contract.Status != "divergence" {
				return nil, fmt.Errorf("ported render mapping %q references %s contract %q", entry.ID, contract.Status, contractID)
			}
		}
	}
	for id := range renderers {
		if !mapped[id] {
			return nil, fmt.Errorf("generated renderer %q has no reviewed mapping", id)
		}
	}
	return dispositions, nil
}

func validHash(value string) bool {
	encoded, ok := strings.CutPrefix(value, "sha256:")
	if !ok || len(encoded) != 64 {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}

func sortedUnique(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i-1] >= values[i] {
			return false
		}
	}
	return true
}

func unique(values []string) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func uniqueAllowEmpty(values []string) bool {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}

func matchesFamily(path string, includes []string) bool {
	for _, include := range includes {
		if path == include || strings.HasSuffix(include, "/") && strings.HasPrefix(path, include) {
			return true
		}
	}
	return false
}

func sourceSegment(root string, source upstreamRange) (string, error) {
	path := filepath.Join(root, filepath.FromSlash(source.Path))
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := string(data)
	start := strings.Index(text, source.Start)
	if start < 0 || strings.Count(text, source.Start) != 1 {
		return "", fmt.Errorf("start marker is missing or ambiguous in %s", source.Path)
	}
	endRelative := strings.Index(text[start+len(source.Start):], source.End)
	if endRelative < 0 || strings.Count(text, source.End) != 1 {
		return "", fmt.Errorf("end marker is missing or ambiguous in %s", source.Path)
	}
	end := start + len(source.Start) + endRelative
	return text[start:end], nil
}

func checkReferences(root, id string, references []string) error {
	for _, reference := range references {
		path, fragment, _ := strings.Cut(reference, "#")
		absolute := filepath.Join(root, filepath.FromSlash(path))
		data, err := os.ReadFile(absolute)
		if err != nil {
			return fmt.Errorf("contract %q reference %q: %w", id, reference, err)
		}
		if fragment != "" && !strings.Contains(string(data), fragment) {
			return fmt.Errorf("contract %q reference %q has no matching fragment", id, reference)
		}
	}
	return nil
}

func findRepoRoot(ledgerPath string) string {
	absolute, err := filepath.Abs(ledgerPath)
	if err != nil {
		return "."
	}
	for directory := filepath.Dir(absolute); ; directory = filepath.Dir(directory) {
		if _, err := os.Stat(filepath.Join(directory, "coding", "upstream.go")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return filepath.Dir(absolute)
		}
	}
}

func containsScenario(references []string) bool {
	for _, reference := range references {
		if strings.HasPrefix(reference, "parity/scenarios/") && strings.HasSuffix(reference, ".toml") {
			return true
		}
	}
	return false
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func stringSetsEqual(a, b []string) bool {
	left := append([]string(nil), a...)
	right := append([]string(nil), b...)
	sort.Strings(left)
	sort.Strings(right)
	return slicesEqual(left, right)
}
