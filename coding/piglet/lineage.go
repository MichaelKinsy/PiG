package piglet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"go.yaml.in/yaml/v3"
)

// LineageLink is one Piglet in an inheritance chain: its identity and what it
// changes against the Piglet it extends. The root has no Changes.
// pig additive (D170): Piglet lineage diagnostics report the chain `extends` already resolves.
type LineageLink struct {
	Name    string       `json:"name"`
	Source  string       `json:"source"`
	Version string       `json:"version,omitempty"`
	Digest  string       `json:"digest"`
	Changes *LinkChanges `json:"changes,omitempty"`
}

// SetChange lists the names a Piglet adds, removes and alters against its base.
type SetChange struct {
	//portlint:allow emptydrop PiG-only format with no Pi counterpart (Pi has no Piglets or runtime slot)
	Added []string `json:"added,omitempty"`
	//portlint:allow emptydrop PiG-only format with no Pi counterpart (Pi has no Piglets or runtime slot)
	Removed []string `json:"removed,omitempty"`
	//portlint:allow emptydrop PiG-only format with no Pi counterpart (Pi has no Piglets or runtime slot)
	Changed []string `json:"changed,omitempty"`
}

func (c SetChange) empty() bool { return len(c.Added)+len(c.Removed)+len(c.Changed) == 0 }

// LinkChanges is the effective difference between a Piglet and its base:
// extensions, packages, skills, built-in tools, permissions (the agent
// environment), slots, strip lists and the remaining settings.
type LinkChanges struct {
	Extensions  SetChange `json:"extensions"`
	Packages    SetChange `json:"packages"`
	Skills      SetChange `json:"skills"`
	Tools       SetChange `json:"tools"`
	Permissions SetChange `json:"permissions"`
	Slots       SetChange `json:"slots"`
	Strip       SetChange `json:"strip"`
	Settings    SetChange `json:"settings"`
}

// Empty reports that the Piglet changes nothing against its base.
func (c LinkChanges) Empty() bool {
	return c.Extensions.empty() && c.Packages.empty() && c.Skills.empty() && c.Tools.empty() && c.Permissions.empty() && c.Slots.empty() && c.Strip.empty() && c.Settings.empty()
}

// LineageReport is the inheritance chain of one source Piglet, root first.
type LineageReport struct {
	Piglet string        `json:"piglet"`
	Source string        `json:"source"`
	Chain  []LineageLink `json:"chain"`
}

type chainLink struct {
	path, identity, digest string
	piglet                 *Piglet
}

// sourceChain walks `extends` from path to the root and returns the links root first.
func sourceChain(path string) ([]chainLink, error) {
	var links []chainLink
	identity := "local:" + filepath.Base(path)
	for depth := 0; ; depth++ {
		if depth > maxExtendsDepth {
			return nil, fmt.Errorf("Piglet extends depth exceeds %d: %s", maxExtendsDepth, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read Piglet %s: %w", path, err)
		}
		p, err := ParseBytes(data)
		if err != nil {
			return nil, fmt.Errorf("Piglet %s: %w", path, err)
		}
		links = append(links, chainLink{path: path, identity: identity, digest: digestPigletBytes(data), piglet: p})
		if p.Extends == nil {
			break
		}
		path, identity, err = resolveExtendsSource(path, p.Extends.Source)
		if err != nil {
			return nil, fmt.Errorf("Piglet %s extends.source: %w", links[len(links)-1].path, err)
		}
	}
	slices.Reverse(links)
	return links, nil
}

// SourceLineage reports the chain of the source Piglet at path with each
// link's change against its base. It reads local files only.
func SourceLineage(path, workspace string) (*LineageReport, error) {
	links, err := sourceChain(path)
	if err != nil {
		return nil, err
	}
	report := &LineageReport{Piglet: links[len(links)-1].piglet.Name, Source: path}
	var parent *Piglet
	for _, link := range links {
		entry := LineageLink{Name: link.piglet.Name, Source: link.identity, Digest: link.digest}
		if link.piglet.Release != nil {
			entry.Version = link.piglet.Release.Version
		}
		resolution, err := ResolveEffectiveWithOptions(link.path, ResolveOptions{Workspace: workspace})
		if err != nil {
			return nil, fmt.Errorf("resolve effective Piglet %s: %w", link.path, err)
		}
		if parent != nil {
			changes := diffPiglets(parent, resolution.Piglet)
			entry.Changes = &changes
		}
		parent = resolution.Piglet
		report.Chain = append(report.Chain, entry)
	}
	return report, nil
}

func diffPiglets(base, child *Piglet) LinkChanges {
	var c LinkChanges
	c.Extensions = diffNamed(namedJSON(base.Extensions, func(e ExtensionEntry) string { return e.Name }), namedJSON(child.Extensions, func(e ExtensionEntry) string { return e.Name }))
	c.Skills = diffNamed(namedJSON(base.Skills, func(e SkillEntry) string { return e.Name }), namedJSON(child.Skills, func(e SkillEntry) string { return e.Name }))
	c.Packages = diffNamed(stringMapJSON(base.Packages), stringMapJSON(child.Packages))
	c.Tools = diffNamed(toolSet(base.BuiltinTools), toolSet(child.BuiltinTools))
	c.Permissions = diffNamed(fieldsJSON(base.AgentEnv), fieldsJSON(child.AgentEnv))
	c.Slots = diffNamed(fieldsJSON(base.Slots), fieldsJSON(child.Slots))
	c.Strip = diffNamed(fieldsJSON(base.Strip), fieldsJSON(child.Strip))
	settings := func(p *Piglet) map[string]string {
		return fieldsJSON(map[string]any{"discovery": p.Discovery, "model": p.Model, "systemPrompt": p.SystemPrompt})
	}
	c.Settings = diffNamed(settings(base), settings(child))
	return c
}

func namedJSON[T any](items []T, name func(T) string) map[string]string {
	out := make(map[string]string, len(items))
	for _, item := range items {
		out[name(item)] = canonicalJSON(item)
	}
	return out
}

func stringMapJSON(m map[string]string) map[string]string { return m }

func toolSet(tools *[]string) map[string]string {
	if tools == nil {
		return map[string]string{"(all built-in tools)": ""}
	}
	out := map[string]string{}
	for _, tool := range *tools {
		out[tool] = ""
	}
	return out
}

// fieldsJSON maps each non-empty top-level field of value, under its YAML key, to its canonical JSON.
func fieldsJSON(value any) map[string]string {
	data, err := yaml.Marshal(value)
	if err != nil {
		return map[string]string{}
	}
	var fields map[string]any
	if yaml.Unmarshal(data, &fields) != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(fields))
	for key, field := range fields {
		if field == nil {
			continue
		}
		if s := canonicalJSON(field); s != `""` && s != "{}" && s != "[]" {
			out[key] = s
		}
	}
	return out
}

// canonicalJSON renders value with its map keys sorted, so equal values compare equal.
func canonicalJSON(value any) string {
	data, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	var generic any
	if yaml.Unmarshal(data, &generic) != nil {
		return string(data)
	}
	//portlint:allow jsonescape PiG-only lineage comparison key with no Pi counterpart (Pi has no Piglets); only compared for equality, never shown
	out, err := json.Marshal(generic)
	if err != nil {
		return string(data)
	}
	return string(out)
}

func diffNamed(base, child map[string]string) SetChange {
	var c SetChange
	for name, value := range child {
		if old, ok := base[name]; !ok {
			c.Added = append(c.Added, name)
		} else if old != value {
			c.Changed = append(c.Changed, name)
		}
	}
	for name := range base {
		if _, ok := child[name]; !ok {
			c.Removed = append(c.Removed, name)
		}
	}
	slices.Sort(c.Added)
	slices.Sort(c.Removed)
	slices.Sort(c.Changed)
	return c
}
