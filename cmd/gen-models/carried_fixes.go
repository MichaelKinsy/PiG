package main

import (
	"regexp"
	"strings"
)

// carriedFix is a Pi fix that landed after the pinned published catalog and that the 0.5.0 release carries forward. The
// generator reads the published package, which predates the fix, so each rule re-applies the fix's effect on the
// catalog rows. Every rule is a no-op on a source that already has the fix: when the pin moves past the commit, delete
// its row (and its rule) and the regenerated catalog does not change.
type carriedFix struct {
	// Sha is the Pi commit the rule carries.
	Sha string
	// Apply edits the catalog in place and returns the rows to keep.
	Apply func(rows []modelRow) []modelRow
}

var carriedFixes = []carriedFix{
	{Sha: "195120a9", Apply: opencodeInferenceEndpoints},
}

// applyCarriedFixes applies every carried fix to the flattened catalog.
func applyCarriedFixes(catalog *modelCatalog) {
	for _, fix := range carriedFixes {
		catalog.Chat = fix.Apply(catalog.Chat)
		catalog.Image = fix.Apply(catalog.Image)
		catalog.Classifier = fix.Apply(catalog.Classifier)
	}
}

var reCompatMember = regexp.MustCompile(`,"compat":\{[^{}]*\}`)

// opencodeInferenceEndpoints is Pi 195120a9 (scripts/generate-models.ts): OpenCode Zen and OpenCode Go move from
// opencode.ai/zen to opencode.ai/inference (and /inference/go); opencode-go minimax-m2.7 is served through the Anthropic
// Messages API (it no longer gets the openai-completions override); opencode-go deepseek-v4-flash is no longer listed.
func opencodeInferenceEndpoints(rows []modelRow) []modelRow {
	const oldBase, newBase = "https://opencode.ai/zen", "https://opencode.ai/inference"
	kept := rows[:0:0]
	for _, row := range rows {
		if row.Provider != "opencode" && row.Provider != "opencode-go" { //portlint:allow providerliteral a carried-fix row names the exact Pi catalog rows it edits
			kept = append(kept, row)
			continue
		}
		if row.Provider == "opencode-go" && row.ID == "deepseek-v4-flash" { //portlint:allow providerliteral a carried-fix row names the exact Pi catalog rows it edits
			continue
		}
		if row.Provider == "opencode-go" && row.ID == "minimax-m2.7" && row.API == "openai-completions" { //portlint:allow providerliteral a carried-fix row names the exact Pi catalog rows it edits
			row.API = "anthropic-messages"
			row.Compat = nil
			row.Record = strings.Replace(row.Record, `"api":"openai-completions"`, `"api":"anthropic-messages"`, 1)
			row.Record = reCompatMember.ReplaceAllString(row.Record, "")
			// The Anthropic SDK appends /v1/messages, so the base URL has no /v1.
			row.BaseURL = strings.TrimSuffix(row.BaseURL, "/v1")
			row.Record = strings.Replace(row.Record, `/zen/go/v1"`, `/zen/go"`, 1)
		}
		row.BaseURL = strings.Replace(row.BaseURL, oldBase, newBase, 1)
		row.Record = strings.ReplaceAll(row.Record, oldBase, newBase)
		kept = append(kept, row)
	}
	return kept
}
