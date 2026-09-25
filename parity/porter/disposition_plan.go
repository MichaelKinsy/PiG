package porter

import (
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding"
)

// DispositionPlan is the deterministic, grouped worklist the Pig Porter emits
// for the state-mapping acceptance ledger. It projects the upstream semantic
// delta down to the changes that are material (a real structural change) and
// land on a file Pig ports (PORT_MAP ✅/🟡), then clusters them by shape-change
// theme so a reviewer dispositions dozens of groups instead of thousands of raw
// interface IDs.
//
// The Porter stays deterministic: it groups candidates and never derives a
// verdict. Recommendation and Decision are empty slots the agent and the human
// fill; ratified dispositions flow through the serialized request-mutation ->
// integrate write path into the state mapping.
type DispositionPlan struct {
	From             string             `json:"from"`
	To               string             `json:"to"`
	TotalChanged     int                `json:"totalChanged"`
	MaterialOnPorted int                `json:"materialOnPorted"`
	Groups           []DispositionGroup `json:"groups"`
}

// DispositionGroup is one shape-change theme across every interface that shares
// it. Recommendation is the agent's proposed disposition (empty from the Porter);
// Decision is the human verdict (empty from the Porter).
type DispositionGroup struct {
	Theme          string   `json:"theme"`
	ChangeKind     string   `json:"changeKind"`
	AddedProps     []string `json:"addedProps,omitempty"`
	RemovedProps   []string `json:"removedProps,omitempty"`
	Count          int      `json:"count"`
	Interfaces     []string `json:"interfaces"`
	Files          []string `json:"files"`
	PortMapStatus  string   `json:"portMapStatus"`
	Recommendation string   `json:"recommendation"`
	Decision       string   `json:"decision"`
}

// dpDecl decodes only the inventory fields the disposition plan needs.
type dpDecl struct {
	ID      string `json:"id"`
	Package string `json:"package"`
	Name    string `json:"name"`
	Source  struct {
		Path string `json:"path"`
	} `json:"source"`
	Shape struct {
		Type       string `json:"type"`
		Properties []struct {
			Name string `json:"name"`
		} `json:"properties"`
	} `json:"shape"`
}

type dpInventory struct {
	Interfaces []dpDecl `json:"interfaces"`
}

type dpManifest struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Changes []struct {
		ID     string `json:"id"`
		Change string `json:"change"`
	} `json:"changes"`
}

// dpPackageSourceRoots mirrors portreconcile.packageSourceRoots: both map the
// pinned upstream npm packages to the monorepo source roots PORT_MAP keys on.
// Kept local to avoid refactoring the portreconcile command's package main for a
// second consumer; extract to a shared package if a third appears.
var dpPackageSourceRoots = map[string]string{
	"@earendil-works/pi-agent-core":   "packages/agent/src",
	"@earendil-works/pi-ai":           "packages/ai/src",
	"@earendil-works/pi-coding-agent": "packages/coding-agent/src",
	"@earendil-works/pi-tui":          "packages/tui/src",
}

var (
	dpDistTypeDecl = regexp.MustCompile(`dist/(.+)\.d\.ts$`)
	dpPortMapRow   = regexp.MustCompile("^\\| `([^`]+)` \\| `([^`]+)` \\| (.+?) \\|")
)

// executeDispositionPlan reads the on-disk semantic delta plus both upstream
// inventories and PORT_MAP under root, then returns the grouped material-on-
// ported worklist. It reads only files; it never touches the closure store.
func executeDispositionPlan(root string) ([]byte, error) {
	interfacesDir := filepath.Join(root, "parity", "interfaces")
	to := coding.UpstreamVersion

	matches, err := filepath.Glob(filepath.Join(interfacesDir, "delta-v*-v"+to+".json"))
	if err != nil {
		return nil, fmt.Errorf("locate semantic delta: %w", err)
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("expected exactly one delta-v*-v%s.json, found %d", to, len(matches))
	}
	manifest, err := dpLoadManifest(matches[0])
	if err != nil {
		return nil, err
	}
	before, err := dpLoadInventory(filepath.Join(interfacesDir, "upstream-v"+manifest.From+".json"))
	if err != nil {
		return nil, err
	}
	after, err := dpLoadInventory(filepath.Join(interfacesDir, "upstream-v"+manifest.To+".json"))
	if err != nil {
		return nil, err
	}
	portmap, err := dpLoadPortMapStatus(filepath.Join(root, "PORT_MAP.md"))
	if err != nil {
		return nil, err
	}

	type agg struct {
		kind   string
		added  []string
		rem    []string
		ifaces map[string]bool
		files  map[string]bool
		status string
	}
	groups := map[string]*agg{}
	totalChanged := 0
	for _, change := range manifest.Changes {
		if change.Change != "changed" {
			continue
		}
		totalChanged++
		a, hadBefore := before[change.ID]
		b, hasAfter := after[change.ID]
		if !hadBefore || !hasAfter {
			continue
		}
		if !dpMaterial(a, b) {
			continue
		}
		src, ok := dpUpstreamSourcePath(b)
		if !ok {
			continue
		}
		status, mapped := portmap[src]
		ported := mapped && (strings.HasPrefix(status, "✅") || strings.HasPrefix(status, "🟡"))
		if !ported {
			continue
		}
		added, removed := dpPropDelta(a, b)
		kind := dpChangeKind(a, b, added, removed)
		key := kind + "\x00" + strings.Join(added, ",") + "\x00" + strings.Join(removed, ",")
		group := groups[key]
		if group == nil {
			group = &agg{kind: kind, added: added, rem: removed, ifaces: map[string]bool{}, files: map[string]bool{}}
			groups[key] = group
		}
		group.ifaces[dpShortName(change.ID)] = true
		group.files[src] = true
		switch {
		case group.status == "":
			group.status = status
		case !strings.HasPrefix(status, group.status[:len("✅")]):
			group.status = "mixed"
		}
	}

	plan := DispositionPlan{From: manifest.From, To: manifest.To, TotalChanged: totalChanged, Groups: []DispositionGroup{}}
	for _, group := range groups {
		count := len(group.ifaces)
		plan.MaterialOnPorted += count
		plan.Groups = append(plan.Groups, DispositionGroup{
			Theme:         dpTheme(group.kind, group.added, group.rem),
			ChangeKind:    group.kind,
			AddedProps:    group.added,
			RemovedProps:  group.rem,
			Count:         count,
			Interfaces:    dpSortedKeys(group.ifaces),
			Files:         dpSortedKeys(group.files),
			PortMapStatus: group.status,
		})
	}
	slices.SortFunc(plan.Groups, func(x, y DispositionGroup) int {
		if c := cmp.Compare(y.Count, x.Count); c != 0 {
			return c
		}
		return cmp.Compare(x.Theme, y.Theme)
	})
	return marshalOutput(plan)
}

func dpLoadManifest(path string) (dpManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return dpManifest{}, fmt.Errorf("read semantic delta: %w", err)
	}
	var manifest dpManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return dpManifest{}, fmt.Errorf("decode semantic delta %s: %w", path, err)
	}
	if manifest.From == "" || manifest.To == "" {
		return dpManifest{}, fmt.Errorf("semantic delta %s missing version range", path)
	}
	return manifest, nil
}

func dpLoadInventory(path string) (map[string]dpDecl, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read inventory: %w", err)
	}
	var inv dpInventory
	if err := json.Unmarshal(data, &inv); err != nil {
		return nil, fmt.Errorf("decode inventory %s: %w", path, err)
	}
	out := make(map[string]dpDecl, len(inv.Interfaces))
	for _, decl := range inv.Interfaces {
		out[decl.ID] = decl
	}
	return out, nil
}

func dpLoadPortMapStatus(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read PORT_MAP: %w", err)
	}
	out := map[string]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		match := dpPortMapRow.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		out[match[1]] = strings.TrimSpace(match[3])
	}
	return out, nil
}

// dpUpstreamSourcePath translates a declaration's published .d.ts location into
// the upstream monorepo source path PORT_MAP keys on. False when the declaration
// cannot be placed (unknown package or non-declaration file).
func dpUpstreamSourcePath(decl dpDecl) (string, bool) {
	base, ok := dpPackageSourceRoots[decl.Package]
	if !ok {
		return "", false
	}
	match := dpDistTypeDecl.FindStringSubmatch(decl.Source.Path)
	if match == nil {
		return "", false
	}
	return base + "/" + match[1] + ".ts", true
}

func dpProps(decl dpDecl) []string {
	names := make([]string, 0, len(decl.Shape.Properties))
	for _, prop := range decl.Shape.Properties {
		names = append(names, prop.Name)
	}
	slices.Sort(names)
	return names
}

// dpMaterial reports whether the structural shape changed, not just the shape
// hash. Hash-only churn (formatting, alias text) is filtered out as noise.
func dpMaterial(a, b dpDecl) bool {
	if a.Shape.Type != b.Shape.Type {
		return true
	}
	return !slices.Equal(dpProps(a), dpProps(b))
}

func dpPropDelta(a, b dpDecl) (added, removed []string) {
	beforeSet := map[string]bool{}
	for _, name := range dpProps(a) {
		beforeSet[name] = true
	}
	afterSet := map[string]bool{}
	for _, name := range dpProps(b) {
		afterSet[name] = true
	}
	for name := range afterSet {
		if !beforeSet[name] {
			added = append(added, name)
		}
	}
	for name := range beforeSet {
		if !afterSet[name] {
			removed = append(removed, name)
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	return added, removed
}

func dpChangeKind(a, b dpDecl, added, removed []string) string {
	switch {
	case len(added) > 0 && len(removed) == 0:
		return "props-added"
	case len(added) == 0 && len(removed) > 0:
		return "props-removed"
	case len(added) > 0 && len(removed) > 0:
		return "props-changed"
	case strings.Contains(b.Shape.Type, "Promise<") && !strings.Contains(a.Shape.Type, "Promise<"):
		return "sync-to-async"
	default:
		return "type-signature"
	}
}

func dpTheme(kind string, added, removed []string) string {
	switch kind {
	case "props-added":
		return "+" + strings.Join(added, ",")
	case "props-removed":
		return "-" + strings.Join(removed, ",")
	case "props-changed":
		return "+" + strings.Join(added, ",") + " -" + strings.Join(removed, ",")
	case "sync-to-async":
		return "sync->async (Promise-returning)"
	default:
		return "type-signature change"
	}
}

// dpShortName returns the declaration name after the package/entrypoint prefix,
// e.g. pkg:ai/.#StreamOptions -> StreamOptions and
// pkg:agent/.#AgentHarness::property:getModel -> AgentHarness::property:getModel.
func dpShortName(id string) string {
	if _, name, ok := strings.Cut(id, "#"); ok {
		return name
	}
	return id
}

func dpSortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}
