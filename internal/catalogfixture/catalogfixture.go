// SPDX-License-Identifier: MIT

// Package catalogfixture is a synthetic model catalog for the benchmarks that measure a function over a catalog (the snapshot copy of internal/codingagent,
// ai's cloneCompat) and must not move when the shipped catalog is refreshed. Only _test.go files import it.
//
// The catalog is LiveEntries entries built from the templates of templates.go: each template is the JSON of one entry of the shipped catalog as it stood on
// 2026-11-16 (car 24 line head 297b8449d51732e22e4f5de2b2469eac144e4dc5, 1,663 entries), and its count is the number of shipped entries it stands for. The
// shipped entries fall into shapes by their compat (none, scalar fields only: few or many, or a map or list), thinking level map (none, one or two levels, more),
// cost tiers, input limits, headers or prompt cache, reasoning and input modalities; a template is the entry of its shape whose copy costs nearest the
// shape's mean, so the count-weighted sum of the templates costs what the shipped catalog costs per entry (17.69 allocations of the snapshot copy per entry
// on 2026-11-16), and a few single-entry templates carry the fields no mean entry sets. Entries of one template differ in their id only.
//
// Live fields the fixture leaves empty: the sampling parameters (SamplingParams and SamplingParamsByThinkingLevel, which no shipped entry sets either) and the
// runtime Provider. Growth of the shipped catalog is tracked elsewhere: the per-entry rows of BenchmarkGeneratedToModelCatalog and BenchmarkModelRegistryMetadata,
// TestCloneCatalogInputLiveAllocationBudget and the startup timing.
package catalogfixture

import (
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/ai"
)

// LiveEntries is the size of the shipped catalog on 2026-11-16, and the size of the fixture.
const LiveEntries = 1663

// template is one shipped entry as JSON and the number of shipped entries it stands for.
type template struct {
	count int
	entry string
}

// GeneratedModels returns the fixture's entries as catalog rows, in template order, each with its own maps, lists and pointers.
func GeneratedModels() []ai.GeneratedModel {
	rows := make([]ai.GeneratedModel, 0, LiveEntries)
	for t, template := range &templates {
		for n := range template.count {
			var row ai.GeneratedModel
			if err := json.Unmarshal([]byte(template.entry), &row); err != nil {
				panic(fmt.Sprintf("catalogfixture: template %d: %v", t, err))
			}
			row.ID = fmt.Sprintf("%s-%d", row.ID, n)
			rows = append(rows, row)
		}
	}
	return rows
}

// Models returns the fixture's entries as the runtime models ToModel makes of them.
func Models() []*ai.Model {
	rows := GeneratedModels()
	models := make([]*ai.Model, len(rows))
	for i := range rows {
		models[i] = rows[i].ToModel()
	}
	return models
}

// Compats returns the compat of each fixture entry, nil for an entry without one.
func Compats() []*ai.ModelCompat {
	rows := GeneratedModels()
	compats := make([]*ai.ModelCompat, len(rows))
	for i := range rows {
		compats[i] = rows[i].Compat
	}
	return compats
}
