package piglet

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/MichaelKinsy/PiG/coding/piglet/artifact"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// pig additive (D92): a Piglet strips built-in tools, slash commands,
// built-in extensions, model APIs and features. A Piglet Binary compiles the
// ones with registration shims out (pig_strip_<id> build tags) and disables
// the rest at runtime. Stock PiG has no Piglet strip list and builds no tag,
// so every built-in stays registered and enabled.

// StripSpec names the built-ins a Piglet strips. Every entry is a stable ID
// from the generated strip ID table (strip_ids_generated.go): a built-in tool
// name, a slash command written with its leading slash, a built-in extension
// name, a model API, or a feature ID.
//
// Each list is in deny mode (the list names what is stripped) or in keep
// mode (Keep names what stays; every other ID of that list in the table is
// stripped, including IDs a later core adds). One list is never in both modes
// in one Piglet file. Parsing expands keep mode against the running core's
// table, so a parsed or resolved spec also carries the stripped IDs of a
// keep-mode list in the deny field, and every consumer reads the deny fields
// alone.
type StripSpec struct {
	Tools      []string   `yaml:"tools,omitempty"`
	Commands   []string   `yaml:"commands,omitempty"`
	Extensions []string   `yaml:"extensions,omitempty"`
	APIs       []string   `yaml:"apis,omitempty"`
	Features   []string   `yaml:"features,omitempty"`
	Keep       *StripKeep `yaml:"keep,omitempty"`
}

// StripKeep names the built-ins each keep-mode list keeps. A nil list is in
// deny mode; an empty list keeps nothing. It has no keep key of its own.
type StripKeep struct {
	Tools      *[]string `yaml:"tools,omitempty"`
	Commands   *[]string `yaml:"commands,omitempty"`
	Extensions *[]string `yaml:"extensions,omitempty"`
	APIs       *[]string `yaml:"apis,omitempty"`
	Features   *[]string `yaml:"features,omitempty"`
}

// fields returns pointers to the five keep lists in lists() order.
func (k *StripKeep) fields() []**[]string {
	return []**[]string{&k.Tools, &k.Commands, &k.Extensions, &k.APIs, &k.Features}
}

// knownStripIDs is the running core's generated strip table. Keep mode
// expands against it. Tests replace it to add a synthetic new ID.
var knownStripIDs = pigstrip.Known

// Strip kinds name the five strip lists in records and `pig piglet show`.
const (
	StripKindTool      = "tool"
	StripKindCommand   = "command"
	StripKindExtension = "extension"
	StripKindAPI       = "api"
	StripKindFeature   = "feature"
)

// Strip dispositions say how a Piglet Binary strips a built-in.
const (
	// StripDispositionRuntime marks a built-in that stays compiled in and is
	// disabled when the Piglet runs.
	StripDispositionRuntime = "runtime"
	// StripDispositionBinary marks a built-in the Piglet Binary does not
	// link. Stock PiG running the Piglet disables it at runtime.
	StripDispositionBinary = "binary"
)

// stripBuildTag returns the build tag that compiles a stripped built-in out
// of a Piglet Binary, and whether the Binary leaves it out at all. Every
// built-in extension and strippable API has registration shims. The
// experimental server needs no tag: stock cmd/pig already leaves it out.
func stripBuildTag(kind, id string) (tag string, binary bool) {
	switch kind {
	case StripKindExtension, StripKindAPI:
		return pigstrip.Tag(id), true
	case StripKindFeature:
		if id == pigstrip.ExperimentalServer {
			return "", true
		}
		if slices.Contains(pigstrip.BinaryFeatures(), id) {
			return pigstrip.Tag(id), true
		}
	}
	return "", false
}

// StripBuildTags lists every build tag a Piglet Binary can be built with,
// sorted: one for each built-in extension, strippable API and compile-out
// feature in the generated ID table.
func StripBuildTags() []string {
	all := StripSpec{Extensions: pigstrip.Known(pigstrip.ListExtensions), APIs: pigstrip.Known(pigstrip.ListAPIs), Features: pigstrip.Known(pigstrip.ListFeatures)}
	return all.BuildTags()
}

// BuildTags returns the sorted build tags that compile the stripped built-ins
// out of a Piglet Binary.
func (s *StripSpec) BuildTags() []string {
	var tags []string
	for _, id := range s.StrippedIDs() {
		if tag, _ := stripBuildTag(id.Kind, id.ID); tag != "" {
			tags = append(tags, tag)
		}
	}
	slices.Sort(tags)
	return slices.Compact(tags)
}

// StrippedID is one stripped built-in.
type StrippedID struct {
	Kind        string
	ID          string
	Disposition string
}

// SlotID names the stripped built-in by its manifest path, the strip list and
// the ID: tools.grep, commands./share, extensions.mcp, features.themes.
func (id StrippedID) SlotID() string {
	for _, list := range (&StripSpec{}).lists() {
		if list.kind == id.Kind {
			return list.field + "." + id.ID
		}
	}
	return id.Kind + "." + id.ID
}

type stripList struct {
	field string
	kind  string
	ids   []string
	known []string
	// keep is the list's keep-mode list, nil in deny mode.
	keep *[]string
}

// lists returns the five lists in manifest order. A nil spec has five empty
// deny-mode lists.
func (s *StripSpec) lists() []stripList {
	var spec StripSpec
	if s != nil {
		spec = *s
	}
	var keep StripKeep
	if spec.Keep != nil {
		keep = *spec.Keep
	}
	return []stripList{
		{field: pigstrip.ListTools, kind: StripKindTool, ids: spec.Tools, known: knownStripIDs(pigstrip.ListTools), keep: keep.Tools},
		{field: pigstrip.ListCommands, kind: StripKindCommand, ids: spec.Commands, known: knownStripIDs(pigstrip.ListCommands), keep: keep.Commands},
		{field: pigstrip.ListExtensions, kind: StripKindExtension, ids: spec.Extensions, known: knownStripIDs(pigstrip.ListExtensions), keep: keep.Extensions},
		{field: pigstrip.ListAPIs, kind: StripKindAPI, ids: spec.APIs, known: knownStripIDs(pigstrip.ListAPIs), keep: keep.APIs},
		{field: pigstrip.ListFeatures, kind: StripKindFeature, ids: spec.Features, known: knownStripIDs(pigstrip.ListFeatures), keep: keep.Features},
	}
}

// fields returns pointers to the five lists in lists() order.
func (s *StripSpec) fields() []*[]string {
	return []*[]string{&s.Tools, &s.Commands, &s.Extensions, &s.APIs, &s.Features}
}

// IsEmpty reports whether the spec strips nothing and keeps no list in keep
// mode. A nil spec is empty.
func (s *StripSpec) IsEmpty() bool {
	return s == nil || len(s.Tools)+len(s.Commands)+len(s.Extensions)+len(s.APIs)+len(s.Features) == 0 && !s.hasKeepMode()
}

// hasKeepMode reports whether any list is in keep mode.
func (s *StripSpec) hasKeepMode() bool {
	if s == nil || s.Keep == nil {
		return false
	}
	for _, list := range s.Keep.fields() {
		if *list != nil {
			return true
		}
	}
	return false
}

// StripIDList is one strip list's IDs: a keep-mode list's kept IDs, or the
// list's part of the generated strip table.
type StripIDList struct {
	Kind string
	IDs  []string
}

// Field names the list as in a Piglet's strip key: tools, commands,
// extensions, apis or features.
func (l StripIDList) Field() string {
	for _, list := range (&StripSpec{}).lists() {
		if list.kind == l.Kind {
			return list.field
		}
	}
	return l.Kind
}

// KeepLists returns each keep-mode list with its kept IDs, in manifest order.
// A spec without keep mode returns nil.
func (s *StripSpec) KeepLists() []StripIDList {
	var out []StripIDList
	for _, list := range s.lists() {
		if list.keep != nil {
			out = append(out, StripIDList{Kind: list.kind, IDs: slices.Clone(*list.keep)})
		}
	}
	return out
}

// StripTable returns the running core's generated strip table, one entry per
// list in manifest order. A Binary record keeps it, so a later core can name
// the built-ins it adds.
func StripTable() []StripIDList {
	var out []StripIDList
	for _, list := range (&StripSpec{}).lists() {
		out = append(out, StripIDList{Kind: list.kind, IDs: list.known})
	}
	return out
}

// HasFeature reports whether the spec names the feature ID. The builder and
// manifest validation read a Piglet's spec with it; the running process asks
// pigstrip.Has, which Record fills.
func (s *StripSpec) HasFeature(id string) bool { return s != nil && slices.Contains(s.Features, id) }

// Record records every entry of the spec in pigstrip, the process's one strip
// state, as a Piglet Binary's OFF shims record the built-ins it compiled out.
// The returned function undoes the records this call added.
func (s *StripSpec) Record() (undo func()) {
	if s.IsEmpty() {
		return func() {}
	}
	var undos []func()
	for _, list := range s.lists() {
		for _, id := range list.ids {
			undos = append(undos, pigstrip.Strip(list.field, id))
		}
	}
	return func() {
		for _, u := range undos {
			u()
		}
	}
}

// StrippedIDs returns every stripped built-in in canonical order: tools,
// commands, extensions, APIs, then features, each sorted by ID, with the
// disposition a Piglet Binary gives it.
func (s *StripSpec) StrippedIDs() []StrippedID {
	if s.IsEmpty() {
		return nil
	}
	var out []StrippedID
	for _, list := range s.lists() {
		for _, id := range slices.Sorted(slices.Values(list.ids)) {
			disposition := StripDispositionRuntime
			if _, binary := stripBuildTag(list.kind, id); binary {
				disposition = StripDispositionBinary
			}
			out = append(out, StrippedID{Kind: list.kind, ID: id, Disposition: disposition})
		}
	}
	return out
}

// validateStrip checks every ID of the deny lists and the keep lists against
// the generated table, and that each keep-mode list carries its expansion.
func validateStrip(location string, strip *StripSpec) error {
	if strip == nil {
		return nil
	}
	for _, list := range strip.lists() {
		if err := validateStripIDs(location+"."+list.field, list.kind, list.ids, list.known); err != nil {
			return err
		}
		if list.keep == nil {
			continue
		}
		if err := validateStripIDs(location+".keep."+list.field, list.kind, *list.keep, list.known); err != nil {
			return err
		}
		if !slices.Equal(slices.Sorted(slices.Values(list.ids)), keepExpansion(list.known, *list.keep)) {
			return fmt.Errorf("%s.%s: keep mode is not expanded against this core's strip table", location, list.field)
		}
	}
	return nil
}

func validateStripIDs(location, kind string, ids, known []string) error {
	seen := make(map[string]struct{}, len(ids))
	for i, id := range ids {
		if id == "" || id != strings.TrimSpace(id) {
			return fmt.Errorf("%s[%d] must be a non-empty ID without surrounding whitespace", location, i)
		}
		if !slices.Contains(known, id) {
			return fmt.Errorf("%s[%d]: unknown %s ID %q (known: %s)", location, i, kind, id, strings.Join(known, ", "))
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("%s[%d] duplicates %q", location, i, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// validateStripModes refuses an authored list in deny mode and keep mode at
// once. It runs on one Piglet file before expansion; after expansion a
// keep-mode list carries its stripped IDs in the deny field by design.
func validateStripModes(location string, strip *StripSpec) error {
	for _, list := range strip.lists() {
		if list.keep != nil && len(list.ids) > 0 {
			return fmt.Errorf("%s.%s and %s.keep.%s: a strip list is in deny mode or keep mode, not both", location, list.field, location, list.field)
		}
	}
	return nil
}

// validateStripKeepNulls refuses a null strip.keep and a null keep list in
// the file as written. The decoder reads a null list as absent, which is
// deny mode and strips nothing, so `extensions:` with no value under keep
// would keep every built-in extension instead of none.
func validateStripKeepNulls(root *yaml.Node) error {
	strip := mappingValue(root, "strip")
	if strip == nil || strip.Kind != yaml.MappingNode {
		return nil
	}
	keep := mappingValue(strip, "keep")
	switch {
	case keep == nil:
		return nil
	case keep.Tag == "!!null":
		return fmt.Errorf("strip.keep must be a mapping of keep-mode lists; write strip.keep.<list>: [] to keep nothing from a list")
	case keep.Kind != yaml.MappingNode:
		return nil
	}
	for i := 0; i+1 < len(keep.Content); i += 2 {
		if keep.Content[i+1].Tag == "!!null" {
			return fmt.Errorf("strip.keep.%s must be a list of the IDs it keeps; write [] to keep nothing", keep.Content[i].Value)
		}
	}
	return nil
}

// mappingValue returns the value node of key in a mapping node, or nil.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// expandKeep sets the deny field of each keep-mode list to the list's IDs in
// the running core's table minus the kept IDs. Every consumer downstream of
// parsing and extends resolution reads the deny fields.
func (s *StripSpec) expandKeep() {
	if !s.hasKeepMode() {
		return
	}
	targets := s.fields()
	for i, list := range s.lists() {
		if list.keep != nil {
			*targets[i] = keepExpansion(list.known, *list.keep)
		}
	}
}

// keepExpansion returns the known IDs not kept, in table order. Nothing
// stripped is nil.
func keepExpansion(known, keep []string) []string {
	var stripped []string
	for _, id := range known {
		if !slices.Contains(keep, id) {
			stripped = append(stripped, id)
		}
	}
	return stripped
}

// inTableOrder returns the IDs of set that the table knows, in table order,
// as a non-nil list: a keep list stays in keep mode when it keeps nothing.
func inTableOrder(known []string, set func(string) bool) []string {
	out := []string{}
	for _, id := range known {
		if set(id) {
			out = append(out, id)
		}
	}
	return out
}

// stripFloorQuit is the slash command that, with every built-in tool, makes
// up the functional floor validateStripFloor keeps.
const stripFloorQuit = "/quit"

// validateStripFloor refuses a strip list that removes every built-in tool
// and /quit together, which would leave a session with neither a tool nor
// the command that ends it. Either alone is allowed: without /quit, Ctrl+D
// and a double Ctrl+C still exit, and a Piglet without tools is a chat-only
// agent. Validate runs on the authored and the effective Piglet, so the rule
// holds for the strip list a lineage unions, at build and at startup alike.
func validateStripFloor(strip *StripSpec) error {
	if strip == nil || !slices.Contains(strip.Commands, stripFloorQuit) {
		return nil
	}
	tools := knownStripIDs(pigstrip.ListTools)
	for _, tool := range tools {
		if !slices.Contains(strip.Tools, tool) {
			return nil
		}
	}
	return fmt.Errorf("strip.tools and strip.commands: a Piglet can't strip every tool and %s (strip.tools: %s; strip.commands: %s); keep at least one tool or %s", stripFloorQuit, strings.Join(tools, ", "), stripFloorQuit, stripFloorQuit)
}

// mergeStrip derives a child's strip spec from its base's, list by list
// (piglet-derivatives design table 2.3):
//
//   - deny base, deny child: base's IDs followed by the child IDs base does
//     not already strip;
//   - keep base K, child strips x: keep K minus x;
//   - keep base K, child keeps K2: keep K2, which must lie inside K;
//   - deny base D, child keeps K2: keep K2, which must not name an ID of D;
//   - keep base, nothing for the list in the child: keep K, so IDs a later
//     core adds stay stripped down the whole chain.
//
// A child keep list naming an ID its base strips is an error that points to
// extends.remove.strip, the one widening path, which applyRemovals has
// already applied to base. An empty result is nil.
func mergeStrip(base, child *StripSpec) (*StripSpec, error) {
	var result StripSpec
	targets := result.fields()
	childLists := child.lists()
	for i, list := range base.lists() {
		own := childLists[i]
		var keep []string
		switch {
		case own.keep != nil:
			for _, id := range *own.keep {
				if slices.Contains(list.ids, id) {
					return nil, fmt.Errorf("strip.keep.%s: %q is stripped by the base; re-enable it with extends.remove.strip.%s and extends.allowWiden", list.field, id, list.field)
				}
			}
			keep = inTableOrder(list.known, func(id string) bool { return slices.Contains(*own.keep, id) })
		case list.keep != nil:
			keep = inTableOrder(list.known, func(id string) bool {
				return slices.Contains(*list.keep, id) && !slices.Contains(own.ids, id)
			})
		default:
			union := slices.Clone(list.ids)
			for _, id := range own.ids {
				if !slices.Contains(union, id) {
					union = append(union, id)
				}
			}
			*targets[i] = union
			continue
		}
		result.setKeep(i, keep)
		*targets[i] = keepExpansion(list.known, keep)
	}
	if result.IsEmpty() {
		return nil, nil
	}
	return &result, nil
}

// setKeep puts the list at index i (lists() order) in keep mode.
func (s *StripSpec) setKeep(i int, keep []string) {
	if s.Keep == nil {
		s.Keep = &StripKeep{}
	}
	*s.Keep.fields()[i] = &keep
}

// removeStrip re-enables the IDs remove names. Each removed ID must be
// stripped. In a keep-mode list the ID joins the keep list, so the list
// stays in keep mode. An empty result is nil.
func removeStrip(strip, remove *StripSpec) (*StripSpec, error) {
	if remove.IsEmpty() {
		return strip, nil
	}
	var result StripSpec
	if strip != nil {
		result = *cloneStrip(strip)
	}
	targets := result.fields()
	lists := result.lists()
	for i, list := range remove.lists() {
		target := targets[i]
		for _, id := range list.ids {
			index := slices.Index(*target, id)
			if index < 0 {
				return nil, fmt.Errorf("remove.strip.%s: %q is absent", list.field, id)
			}
			*target = slices.Delete(*target, index, index+1)
		}
		if keep := lists[i].keep; keep != nil && len(list.ids) > 0 {
			result.setKeep(i, inTableOrder(list.known, func(id string) bool {
				return slices.Contains(*keep, id) || slices.Contains(list.ids, id)
			}))
		}
	}
	if result.IsEmpty() {
		return nil, nil
	}
	return &result, nil
}

// validateRemoveStrip refuses extends.remove.strip.keep: a child cannot
// return a keep-mode list to deny mode, which would admit every built-in a
// later core adds. Single IDs come back with extends.remove.strip.<list>.
func validateRemoveStrip(remove *StripSpec) error {
	for _, list := range remove.lists() {
		if list.keep != nil {
			return fmt.Errorf("extends.remove.strip.keep.%s: a child can't return the keep-mode list %s to deny mode; re-enable single IDs with extends.remove.strip.%s", list.field, list.field, list.field)
		}
	}
	return nil
}

// stripWidening names each inherited strip entry remove re-enables.
func stripWidening(remove *StripSpec) []string {
	if remove == nil {
		return nil
	}
	var widened []string
	for _, list := range remove.lists() {
		for _, id := range list.ids {
			widened = append(widened, "strip."+list.field+"/"+id)
		}
	}
	return widened
}

func cloneStrip(strip *StripSpec) *StripSpec {
	if strip == nil {
		return nil
	}
	var out StripSpec
	targets := out.fields()
	for i, list := range strip.lists() {
		*targets[i] = slices.Clone(list.ids)
		if list.keep != nil {
			out.setKeep(i, slices.Clone(*list.keep))
		}
	}
	return &out
}

// MarshalYAML writes the spec as a Piglet file states it: a keep-mode list
// writes its keep list and not its expansion, so a baked Piglet re-expands
// against its Binary's own table and parses again.
func (s StripSpec) MarshalYAML() (any, error) {
	type plain StripSpec
	out := plain(*cloneStrip(&s))
	targets := (*StripSpec)(&out).fields()
	for i, list := range s.lists() {
		if list.keep != nil {
			*targets[i] = nil
		}
	}
	return out, nil
}

// recordStrip converts a Binary record's strip entries.
func recordStrip(entries []artifact.StripEntry) []StrippedID {
	if len(entries) == 0 {
		return nil
	}
	ids := make([]StrippedID, len(entries))
	for i, entry := range entries {
		ids[i] = StrippedID(entry)
	}
	return ids
}

// recordStripKeep converts a Binary record's keep-mode strip lists.
func recordStripKeep(lists []artifact.StripIDs) []StripIDList {
	if len(lists) == 0 {
		return nil
	}
	out := make([]StripIDList, len(lists))
	for i, list := range lists {
		out[i] = StripIDList(list)
	}
	return out
}
