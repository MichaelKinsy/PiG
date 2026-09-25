package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
)

type inventory struct {
	UpstreamVersion string            `json:"upstreamVersion"`
	Interfaces      []interfaceRecord `json:"interfaces"`
}

type interfaceRecord struct {
	ID        string `json:"id"`
	ShapeHash string `json:"shapeHash"`
}

type manifest struct {
	From    string   `json:"from"`
	To      string   `json:"to"`
	Changes []change `json:"changes"`
}

type change struct {
	ID              string   `json:"id"`
	Change          string   `json:"change"`
	BeforeShapeHash string   `json:"before_shape_hash,omitempty"`
	AfterShapeHash  string   `json:"after_shape_hash,omitempty"`
	Disposition     string   `json:"disposition"`
	Evidence        []string `json:"evidence"`
	Rationale       string   `json:"rationale"`
}

func main() {
	fromInventory := flag.String("from-inventory", "", "previous package interface inventory")
	fromObservable := flag.String("from-observable", "", "previous observable interface inventory")
	toInventory := flag.String("to-inventory", "", "current package interface inventory")
	toObservable := flag.String("to-observable", "", "current observable interface inventory")
	manifestPath := flag.String("manifest", "", "reviewed semantic delta manifest")
	generate := flag.Bool("generate", false, "print an all-pending semantic delta manifest")
	strict := flag.Bool("strict", false, "reject pending semantic changes")
	flag.Parse()

	beforeVersion, before, err := loadCombined(*fromInventory, *fromObservable)
	if err != nil {
		fatal(err)
	}
	afterVersion, after, err := loadCombined(*toInventory, *toObservable)
	if err != nil {
		fatal(err)
	}
	expected := computeDelta(before, after)
	if *generate {
		out := manifest{From: beforeVersion, To: afterVersion, Changes: expected}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(out); err != nil {
			fatal(err)
		}
		return
	}
	if *manifestPath == "" {
		fatal(fmt.Errorf("-manifest is required unless -generate is set"))
	}
	var got manifest
	if err := decode(*manifestPath, &got); err != nil {
		fatal(err)
	}
	problems := validate(got, beforeVersion, afterVersion, expected, *strict)
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintln(os.Stderr, "interface-delta:", problem)
		}
		os.Exit(1)
	}
	fmt.Printf("interface-delta: clean (%s -> %s, %d semantic changes)\n", got.From, got.To, len(got.Changes))
}

func loadCombined(packagePath, observablePath string) (string, map[string]string, error) {
	if packagePath == "" || observablePath == "" {
		return "", nil, fmt.Errorf("package and observable inventory paths are required")
	}
	var packages, observable inventory
	if err := decodeInventory(packagePath, &packages); err != nil {
		return "", nil, err
	}
	if err := decodeInventory(observablePath, &observable); err != nil {
		return "", nil, err
	}
	if packages.UpstreamVersion == "" || packages.UpstreamVersion != observable.UpstreamVersion {
		return "", nil, fmt.Errorf("inventory version mismatch %q/%q", packages.UpstreamVersion, observable.UpstreamVersion)
	}
	out := make(map[string]string, len(packages.Interfaces)+len(observable.Interfaces))
	for _, record := range append(packages.Interfaces, observable.Interfaces...) {
		if record.ID == "" || record.ShapeHash == "" {
			return "", nil, fmt.Errorf("inventory contains empty id or shape hash")
		}
		if _, exists := out[record.ID]; exists {
			return "", nil, fmt.Errorf("duplicate interface id %q", record.ID)
		}
		out[record.ID] = record.ShapeHash
	}
	return packages.UpstreamVersion, out, nil
}

func computeDelta(before, after map[string]string) []change {
	ids := make([]string, 0, len(before)+len(after))
	seen := map[string]bool{}
	for id := range before {
		seen[id] = true
		ids = append(ids, id)
	}
	for id := range after {
		if !seen[id] {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	out := make([]change, 0)
	for _, id := range ids {
		oldHash, hadOld := before[id]
		newHash, hasNew := after[id]
		item := change{ID: id, Disposition: "pending", Evidence: []string{}, Rationale: ""}
		switch {
		case !hadOld:
			item.Change, item.AfterShapeHash = "added", newHash
		case !hasNew:
			item.Change, item.BeforeShapeHash = "removed", oldHash
		case oldHash != newHash:
			item.Change, item.BeforeShapeHash, item.AfterShapeHash = "changed", oldHash, newHash
		default:
			continue
		}
		out = append(out, item)
	}
	return out
}

func validate(got manifest, from, to string, expected []change, strict bool) []string {
	var problems []string
	if got.From != from || got.To != to {
		problems = append(problems, fmt.Sprintf("version range = %q -> %q, want %q -> %q", got.From, got.To, from, to))
	}
	if len(got.Changes) != len(expected) {
		problems = append(problems, fmt.Sprintf("changes = %d, want %d", len(got.Changes), len(expected)))
	}
	limit := min(len(got.Changes), len(expected))
	for i := range limit {
		actual, want := got.Changes[i], expected[i]
		if actual.ID != want.ID || actual.Change != want.Change || actual.BeforeShapeHash != want.BeforeShapeHash || actual.AfterShapeHash != want.AfterShapeHash {
			problems = append(problems, fmt.Sprintf("change[%d] identity/shape drift for %q", i, actual.ID))
			continue
		}
		switch actual.Disposition {
		case "pending", "ported", "partial", "deferred", "designed-out", "divergence", "removed-replaced":
		default:
			problems = append(problems, fmt.Sprintf("%s has invalid disposition %q", actual.ID, actual.Disposition))
		}
		if strict && (actual.Disposition == "pending" || actual.Disposition == "partial") {
			problems = append(problems, fmt.Sprintf("%s remains %s", actual.ID, actual.Disposition))
		}
		if actual.Disposition != "pending" && (len(actual.Evidence) == 0 || actual.Rationale == "") {
			problems = append(problems, fmt.Sprintf("%s disposition %s needs evidence and rationale", actual.ID, actual.Disposition))
		}
	}
	return problems
}

func decode(path string, target any) error {
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
		if err == nil {
			return fmt.Errorf("decode %s: trailing JSON", path)
		}
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func decodeInventory(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "interface-delta:", err)
	os.Exit(2)
}
