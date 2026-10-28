package piglet

import (
	"fmt"
	"slices"
	"strings"
)

// OverlayBase is the Piglet a Piglet Binary that sets build.allowOverlays bakes, offered as the implicit base of the Piglet
// a user selects at launch (--piglet, PIG_PIGLET_PATH, PIG_PIGLET_NAME).
//
// pig additive (D202): a Piglet Binary can declare that it accepts overlay Piglets.
type OverlayBase struct {
	// Piglet is the Binary's baked Piglet. Resolution never modifies it. When the Binary has a frontend member compiled in,
	// Slots.Frontend names it (the baked bytes carry no slots), so extends.remove.slots can start the Binary without it.
	Piglet *Piglet
	// Digest is the digest of the baked Piglet's bytes, the identity the Binary's record and signed manifest vouch for.
	Digest string
}

// resolveOverlayRoot merges the root of an overlay's extends chain onto the Binary's baked Piglet exactly as extends merges
// a child onto its base. The root is the Piglet file with no extends block, or whose extends block names no source: that is
// the form that spells extends.remove and extends.allowWiden for an overlay that has no other base than the Binary. What
// the Binary compiled in is checked once on the final effective Piglet (checkOverlay), so a later link of the overlay's own
// chain cannot undo it.
func resolveOverlayRoot(p *Piglet, entry LineageEntry, digest string, base *OverlayBase) (*EffectiveResolution, error) {
	if p.Extends == nil {
		p.Extends = &ExtendsSpec{}
	}
	effective, err := mergePiglets(base.Piglet, p)
	if err != nil {
		return nil, fmt.Errorf("derive Piglet %s from Piglet Binary %q: %w", p.sourcePath, base.Piglet.Name, err)
	}
	if err := effective.Validate(); err != nil {
		return nil, fmt.Errorf("effective Piglet %s: %w", p.sourcePath, err)
	}
	baseEntry := LineageEntry{Source: "binary:" + base.Piglet.Name, Digest: base.Digest}
	return &EffectiveResolution{Piglet: effective, Lineage: []LineageEntry{baseEntry, entry}, SourceDigest: digest}, nil
}

// checkOverlay refuses an overlay that changes what is compiled into the Binary, the only part an overlay cannot change.
// The overlay otherwise merges as an extends child merges onto its base (the system prompt replaces, extends.remove removes
// members, extends.allowWiden widens), and those rules were applied while resolving. Each message names the field and the
// Binary and says why it cannot change.
func checkOverlay(base, effective *Piglet) error {
	binary := fmt.Sprintf("Piglet Binary %q", base.Name)
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if effective.Slots != nil && effective.Slots.Frontend != nil {
		switch {
		case base.Slots == nil || base.Slots.Frontend == nil:
			add("slots.frontend: %s was built without a frontend member, and a frontend is Go code compiled into the Binary; an overlay cannot set one", binary)
		case base.Slots.Frontend.Member != effective.Slots.Frontend.Member:
			add("slots.frontend: %s has its frontend member compiled in; an overlay cannot replace it with another (extends.remove.slots: [frontend] starts the Binary without it)", binary)
		}
	}
	// A baked extension is fused into the Binary or embedded in it as a prebuilt cell, and the Binary selects it by name. An
	// entry with origins under that name would name another member, which the Binary cannot build at run time in its place.
	for _, name := range extensionNames(base.Extensions) {
		if entry := findNamed(effective.Extensions, name, func(v ExtensionEntry) string { return v.Name }); len(entry.Origins) > 0 {
			add("extensions[%s]: %s has this member compiled in (fused or embedded); an overlay cannot replace it with another one under the same name (extends.remove.extensions removes it; give the new one another name)", name, binary)
		}
	}
	// A built-in a strip build tag compiled out is not linked into the Binary, so no overlay can switch it on; a runtime strip
	// entry (a tool, a command, a runtime-only feature) is only disabled when the Binary starts, and an overlay can remove it.
	effectiveStrip := effective.Strip.lists()
	for i, list := range base.Strip.lists() {
		for _, id := range list.ids {
			if tag, compiledOut := stripBuildTag(list.kind, id); compiledOut && !slices.Contains(effectiveStrip[i].ids, id) {
				add("strip.%s: %q is compiled out of %s (build tag %s), so its code is not in the Binary; an overlay cannot re-enable it", list.field, id, binary, tag)
			}
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(problems, "; "))
}

func extensionNames(entries []ExtensionEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name
	}
	return names
}

// findNamed returns the entry with the name, or the zero value.
func findNamed[T any](items []T, name string, identity func(T) string) T {
	for _, item := range items {
		if identity(item) == name {
			return item
		}
	}
	var zero T
	return zero
}

// unchangedBakedSkills names the baked skills the effective Piglet carries unchanged.
func unchangedBakedSkills(base, effective *Piglet) map[string]bool {
	out := map[string]bool{}
	for _, skill := range base.Skills {
		if got := findNamed(effective.Skills, skill.Name, func(v SkillEntry) string { return v.Name }); got.Name != "" && got.Content == skill.Content && got.Description == skill.Description && len(got.Origins) == 0 {
			out[skill.Name] = true
		}
	}
	return out
}
