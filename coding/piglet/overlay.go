package piglet

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// OverlayBase is the Piglet a Piglet Binary that sets build.allowOverlays bakes, offered as the implicit base of the Piglet
// a user selects at launch (--piglet, PIG_PIGLET_PATH, PIG_PIGLET_NAME).
//
// pig additive (D202): a Piglet Binary can declare that it accepts overlay Piglets.
type OverlayBase struct {
	// Piglet is the Binary's baked Piglet. Resolution never modifies it.
	Piglet *Piglet
	// Digest is the digest of the baked Piglet's bytes, the identity the Binary's record and signed manifest vouch for.
	Digest string
}

// resolveOverlayRoot merges the root of an overlay's extends chain (the Piglet file with no extends of its own) onto the
// Binary's baked Piglet with the same merge as extends. Widening, replacement and removal are checked once on the final
// effective Piglet (checkOverlay), so a later link of the overlay's own chain cannot undo them; the root merge therefore
// states allowWiden and defers.
func resolveOverlayRoot(p *Piglet, entry LineageEntry, digest string, base *OverlayBase) (*EffectiveResolution, error) {
	p.Extends = &ExtendsSpec{AllowWiden: true}
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

// checkOverlay refuses an overlay that changes what the Binary baked. The effective Piglet may add to the base and narrow
// it; it may not replace or remove a member, set a slot or the system prompt, widen a ceiling, or re-enable a stripped
// built-in. Each message names the field and the Binary.
func checkOverlay(base, effective *Piglet) error {
	binary := fmt.Sprintf("Piglet Binary %q", base.Name)
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	if effective.Slots != nil && effective.Slots.Frontend != nil {
		add("slots.frontend: %s fuses its slots when it is built; an overlay cannot set a frontend member", binary)
	}
	if !reflect.DeepEqual(base.SystemPrompt, effective.SystemPrompt) {
		add("systemPrompt: the baseline system prompt of %s cannot be replaced; use --append-system-prompt for an additive prompt", binary)
	}
	for _, kind := range []struct {
		field string
		base  []string
		eff   []string
		same  func(name string) bool
	}{
		{"extensions", extensionNames(base.Extensions), extensionNames(effective.Extensions), func(name string) bool {
			return reflect.DeepEqual(findNamed(base.Extensions, name, func(v ExtensionEntry) string { return v.Name }), findNamed(effective.Extensions, name, func(v ExtensionEntry) string { return v.Name }))
		}},
		{"skills", skillNames(base.Skills), skillNames(effective.Skills), func(name string) bool {
			return reflect.DeepEqual(findNamed(base.Skills, name, func(v SkillEntry) string { return v.Name }), findNamed(effective.Skills, name, func(v SkillEntry) string { return v.Name }))
		}},
	} {
		for _, name := range kind.base {
			switch {
			case !slices.Contains(kind.eff, name):
				add("%s[%s]: %s carries it; an overlay cannot remove it", kind.field, name, binary)
			case !kind.same(name):
				add("%s[%s]: %s already carries one with this name; an overlay cannot replace it (give the overlay's entry another name)", kind.field, name, binary)
			}
		}
	}
	if allowlistWidens(base.BuiltinTools, effective.BuiltinTools) {
		add("tools: an overlay cannot widen the built-in tools of %s%s; rebuild the Binary's Piglet to widen", binary, listSuffix(base.BuiltinTools))
	}
	if discoveryWidens(base.Discovery, effective.Discovery) {
		add("discovery: an overlay cannot add ambient Resource scopes %s does not allow; rebuild the Binary's Piglet to widen", binary)
	}
	if agentEnvironmentWidens(base.AgentEnv, effective.AgentEnv) {
		add("agentEnv: an overlay cannot widen the agent environment of %s; rebuild the Binary's Piglet to widen", binary)
	}
	for _, secret := range base.Secrets {
		if !slices.ContainsFunc(effective.Secrets, func(s SecretDeclaration) bool { return reflect.DeepEqual(s, secret) }) {
			add("secrets[%s]: %s requires it; an overlay cannot drop or change it", secret.Name, binary)
		}
	}
	effectiveStrip := effective.Strip.lists()
	for i, list := range base.Strip.lists() {
		for _, id := range list.ids {
			if !slices.Contains(effectiveStrip[i].ids, id) {
				add("strip.%s: %q is stripped by %s; an overlay cannot re-enable it", list.field, id, binary)
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

func skillNames(entries []SkillEntry) []string {
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

func listSuffix(values *[]string) string {
	if values == nil {
		return ""
	}
	return " (" + strings.Join(*values, ", ") + ")"
}
