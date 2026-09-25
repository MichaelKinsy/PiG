package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode"

	"github.com/MichaelKinsy/PiG/coding"
)

type semanticInventory struct {
	UpstreamVersion string          `json:"upstreamVersion"`
	Interfaces      []semanticEntry `json:"interfaces"`
}

type semanticEntry struct {
	ID        string `json:"id"`
	ParentID  string `json:"parentId"`
	Role      string `json:"role"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	ShapeHash string `json:"shapeHash"`
	Flag      string `json:"flag"`
}

type goInventory struct {
	Interfaces []goEntry `json:"interfaces"`
}

type goEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	ShapeHash string `json:"shapeHash"`
}

type recommendationLedger struct {
	UpstreamVersion string           `json:"upstreamVersion"`
	GeneratedBy     string           `json:"generatedBy"`
	Recommendations []recommendation `json:"recommendations"`
}

type recommendation struct {
	ID                     string   `json:"id"`
	UpstreamShapeHash      string   `json:"upstreamShapeHash"`
	RecommendedDisposition string   `json:"recommendedDisposition"`
	Confidence             string   `json:"confidence"`
	Basis                  []string `json:"basis"`
	Alternatives           []string `json:"alternatives"`
	PigCandidates          []string `json:"pigCandidates"`
	MissingClosure         []string `json:"missingClosure"`
	Provenance             []string `json:"provenance"`
}

func main() {
	upstreamPath := flag.String("inventory", "parity/interfaces/upstream-v"+coding.UpstreamVersion+".json", "upstream package inventory")
	observablePath := flag.String("observable", "", "observable inventory")
	goPath := flag.String("go-inventory", "parity/interfaces/pig-go.json", "Pig Go inventory")
	out := flag.String("out", "-", "output path or -")
	flag.Parse()

	var upstream semanticInventory
	mustDecode(*upstreamPath, &upstream)
	if *observablePath != "" {
		var observable semanticInventory
		mustDecode(*observablePath, &observable)
		upstream.Interfaces = append(upstream.Interfaces, observable.Interfaces...)
	}
	var pig goInventory
	mustDecode(*goPath, &pig)
	ledger := recommend(upstream, pig)
	var writer = os.Stdout
	if *out != "-" {
		file, err := os.Create(*out)
		if err != nil {
			panic(err)
		}
		defer func() { _ = file.Close() }()
		writer = file
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(ledger); err != nil {
		panic(err)
	}
}

func mustDecode(path string, target any) {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		panic(err)
	}
}

func recommend(upstream semanticInventory, pig goInventory) recommendationLedger {
	byName := map[string][]goEntry{}
	for _, entry := range pig.Interfaces {
		key := normalizedName(entry.Name)
		byName[key] = append(byName[key], entry)
	}
	ledger := recommendationLedger{UpstreamVersion: upstream.UpstreamVersion, GeneratedBy: "pig-interface-recommend-v2"}
	for _, entry := range upstream.Interfaces {
		item := recommendation{
			ID: entry.ID, UpstreamShapeHash: entry.ShapeHash,
			RecommendedDisposition: "pending", Confidence: "low",
			Alternatives:   []string{"ported", "partial", "deferred", "designed-out", "divergence"},
			PigCandidates:  []string{},
			MissingClosure: []string{"production-reachability", "required-layers", "behavioral-evidence"},
			Provenance:     []string{"generator:pig-interface-recommend-v2", "upstream:" + upstream.UpstreamVersion},
		}
		switch {
		case strings.HasPrefix(entry.ID, "cli:"):
			item.Basis = []string{"Pig CLI semantic candidate extraction is not implemented yet."}
			item.MissingClosure = append([]string{"pig-cli-shape"}, item.MissingClosure...)
		case entry.ParentID != "":
			item.Basis = []string{"Member-level candidates require parent-aware shape translation; leaf-name matching is disabled."}
			item.MissingClosure = append([]string{"candidate-disambiguation", "shape-translation-review"}, item.MissingClosure...)
		default:
			candidates := byName[normalizedName(entry.Name)]
			for _, candidate := range candidates {
				item.PigCandidates = append(item.PigCandidates, candidate.ID)
			}
			slices.Sort(item.PigCandidates)
			switch {
			case len(candidates) == 0:
				item.Basis = []string{"No normalized-name candidate exists in the scanned Pig production packages."}
				item.MissingClosure = append([]string{"pig-go-shape"}, item.MissingClosure...)
			case len(candidates) == 1:
				item.RecommendedDisposition = "partial"
				item.Confidence = "low"
				item.Alternatives = []string{"ported", "pending", "deferred", "designed-out", "divergence"}
				item.Basis = []string{"Exactly one normalized declared Go symbol name matches; package context, semantic shape, and production closure remain unverified."}
				item.MissingClosure = append([]string{"shape-translation-review"}, item.MissingClosure...)
			default:
				item.Basis = []string{fmt.Sprintf("%d normalized-name Pig candidates are ambiguous.", len(candidates))}
				item.MissingClosure = append([]string{"candidate-disambiguation", "shape-translation-review"}, item.MissingClosure...)
			}
		}
		ledger.Recommendations = append(ledger.Recommendations, item)
	}
	slices.SortFunc(ledger.Recommendations, func(a, b recommendation) int { return strings.Compare(a.ID, b.ID) })
	return ledger
}

func normalizedName(value string) string {
	if index := strings.LastIndex(value, "."); index >= 0 {
		value = value[index+1:]
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}
