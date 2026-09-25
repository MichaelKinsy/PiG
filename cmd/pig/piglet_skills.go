package main

import (
	"errors"
	"fmt"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// skillLoadResult holds the outcome of resolving and loading all skills for a
// session: from the piglet (if any), --skill flags, and convention dirs.
type skillLoadResult struct {
	// Defs are the fully-loaded skill definitions, ready for the system prompt
	// and /skill: command registration.
	Defs []*codingagent.SkillDef
	// Paths are the source paths from which Defs were loaded (for the
	// SkillPaths interactive-mode option and hot-reload).
	Paths []string
}

// resolveAndLoadSkills resolves and loads every skill for the session.
//
// When a piglet declares skills, they are resolved and loaded *in addition
// to* (not instead of) the marketplace/convention skills that
// collectSkillInputs already gathered. This is the union semantics documented
// in the adwaraki piglet:
//
//	"Marketplace skills ... are inherited globally via settings.json packages:,
//	 so they load for any piglet ... without being redeclared here."
//
// If a piglet skill has the same name as a marketplace skill, the piglet
// version wins (it is the explicit declaration). Deduplication is by name,
// not by path: the two sources have different paths for the same skill.
//
// Extracted from main() so the piglet-skills → loadSkills wiring: the site
// of a bug where NoSkills was passed as loadSkills's kill-switch: is unit
// testable. The loadSkills call inside this function always passes false for
// noSkills: we always want to load explicitly-resolved inputs.
func resolveAndLoadSkills(p *piglet.Piglet, collectedSkillInputs []string) (*skillLoadResult, error) {
	if p == nil || len(p.Skills) == 0 {
		// No piglet skills: just load whatever collectSkillInputs gathered.
		defs, err := loadSkills(collectedSkillInputs, false)
		return &skillLoadResult{Defs: defs, Paths: collectedSkillInputs}, err
	}

	// Piglet declares skills: resolve origins and build the piglet skill
	// inputs (filesystem paths + inline content).
	resolved, errs := piglet.ResolveSkills(p)
	if len(errs) > 0 {
		return nil, fmt.Errorf("resolve Piglet skills: %w", errors.Join(errs...))
	}

	var pigletSkillInputs []string
	var inlineDefs []*codingagent.SkillDef
	for _, s := range p.Skills {
		if s.Content == "" {
			continue
		}
		inlineDefs = append(inlineDefs, &codingagent.SkillDef{
			Name:        s.Name,
			Description: s.Description,
			Body:        s.Content,
		})
	}
	for _, s := range resolved {
		pigletSkillInputs = append(pigletSkillInputs, s.Path)
	}

	// Union: load marketplace/convention skills first, then piglet skills on
	// top. Deduplicate by NAME (not path) so the piglet version wins for
	// overlaps. Load each set separately so ambient first-wins collision
	// handling does not suppress an explicit Piglet selection.
	marketDefs, err := loadSkills(collectedSkillInputs, false)
	if err != nil {
		return nil, err
	}
	pigletDefs, err := loadSkills(pigletSkillInputs, false)
	if err != nil {
		return nil, err
	}

	// Build a name→def map. Start with marketplace skills, then overlay
	// piglet skills (piglet wins on name conflict).
	byName := make(map[string]*codingagent.SkillDef, len(marketDefs)+len(pigletDefs))
	var orderedNames []string
	for _, d := range marketDefs {
		if _, exists := byName[d.Name]; !exists {
			byName[d.Name] = d
			orderedNames = append(orderedNames, d.Name)
		}
	}
	for _, d := range pigletDefs {
		if _, exists := byName[d.Name]; !exists {
			// New piglet-only skill: add it (keep stable ordering).
			byName[d.Name] = d
			orderedNames = append(orderedNames, d.Name)
		} else {
			// Name conflict: piglet version wins (replace).
			byName[d.Name] = d
		}
	}
	for _, d := range inlineDefs {
		if _, exists := byName[d.Name]; !exists {
			orderedNames = append(orderedNames, d.Name)
		}
		byName[d.Name] = d
	}

	// Assemble the final deduplicated list in stable order.
	var defs []*codingagent.SkillDef
	for _, name := range orderedNames {
		defs = append(defs, byName[name])
	}

	// SkillPaths: union of marketplace + piglet paths, for /reload.
	allPaths := dedupStrings(append(append([]string{}, pigletSkillInputs...), collectedSkillInputs...))

	return &skillLoadResult{Defs: defs, Paths: allPaths}, nil
}
