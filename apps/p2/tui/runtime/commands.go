package runtime

// One registry drives keys, palette, slash commands and help (p2-tui-port.md section 6). The dispatcher implements stages 3-4 of the section 6.2
// precedence contract inside the worker; stage 1 is the launcher, stage 2 the alt screen (which runs before the focused component), stage 5 the focused
// component.
//
// Forced Go differences: a command's Run and a completer are blocking calls returning an error (Pi's are Promise-returning); a slash expansion returns
// ("", false) where Pi's returns undefined.
//
// pi: apps/p2/src/runtime/tui-commands.ts

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// TuiCommandSlash is a command's slash form.
type TuiCommandSlash struct {
	Name         string
	ArgumentHint string
	// Complete completes the argument prefix; nil items mean none.
	Complete func(prefix string, ctx TuiContext) ([]pitui.AutocompleteItem, error)
	// TabOnly: arguments complete on Tab only, never while typing (paths: Enter must submit what was typed).
	TabOnly bool
}

// TuiCommand is one command of the registry.
type TuiCommand struct {
	Title string
	// Category groups /hotkeys and the palette ("Session", "View", ...).
	Category string
	// Keys are the default keys for plugin ids. Ids from pi's built-in table (`app.*`) always use pi's defaults.
	Keys []string
	// When is a boolean context expression, e.g. "chat && editorEmpty && !overlay". More terms = more specific.
	When  string
	Slash *TuiCommandSlash
	// HiddenFromPalette is `palette: false` (still in /hotkeys).
	HiddenFromPalette bool
	Run               func(ctx TuiContext, args string) error
}

// TuiCommands is the command registry. Key = command id: pi ids where the semantics match, otherwise `<plugin>.<verb>`.
var TuiCommands = p2runtime.NewKeyedRegistry[TuiCommand]("p2.tui-commands")

// TuiSlashEntry is a slash name that is not a command: a prompt template (`name`) or a skill (`skill:name`).
type TuiSlashEntry struct{ Name, Description, ArgumentHint string }

// TuiSlashSource is more slash names for completion and for the editor's submit (10.8: templates and skills from the prompt plugin). Commands always
// win: an entry with a command's slash name is neither listed nor expanded.
type TuiSlashSource interface {
	Entries(ctx TuiContext) []TuiSlashEntry
	// Expand is the text to send instead of text (`/name args`), or ("", false) to leave it as typed.
	Expand(ctx TuiContext, text string) (string, bool, error)
}

// TuiSlashSources is the slash source registry.
var TuiSlashSources = p2runtime.NewKeyedRegistry[TuiSlashSource]("p2.tui-slash-sources")

// TuiCommandConflictKind is the kind of a TuiCommandConflict.
type TuiCommandConflictKind string

const (
	ConflictPrivileged TuiCommandConflictKind = "privileged"
	ConflictTranscript TuiCommandConflictKind = "transcript"
	ConflictCommand    TuiCommandConflictKind = "command"
	ConflictSlash      TuiCommandConflictKind = "slash"
	ConflictInvalid    TuiCommandConflictKind = "invalid"
)

// TuiCommandConflict is one thing the rebuild reports to /hotkeys.
type TuiCommandConflict struct {
	Kind    TuiCommandConflictKind
	Message string
}

type commandEntry struct {
	id       string
	command  TuiCommand
	when     ParsedWhen
	order    int
	userBound bool
	slash    string
}

// transcriptKeys are the keys the alt screen consumes before the focused component (stage 2), unless an overlay defers.
var transcriptKeys = []string{
	"tui.altScreen.search",
	"tui.altScreen.pageUp",
	"tui.altScreen.pageDown",
	"tui.altScreen.halfPageUp",
	"tui.altScreen.halfPageDown",
	"tui.altScreen.lineUp",
	"tui.altScreen.lineDown",
	"tui.altScreen.previousPrompt",
	"tui.altScreen.nextPrompt",
	"tui.altScreen.top",
	"tui.altScreen.bottom",
}

var (
	commandIDPattern = regexp.MustCompile(`^[a-z][\w-]*(?:\.[A-Za-z][\w-]*)+$`)
	slashNamePattern = regexp.MustCompile(`^[a-z][\w:-]{0,39}$`)
)

// TuiCommandDispatcherOptions are the dispatcher's hooks into its runtime.
type TuiCommandDispatcherOptions struct {
	Ctx       func() TuiContext
	Contexts  func() map[string]bool
	Exclusive func() [][]string
	OnError   func(err error)
	OnRebuild func()
}

// TuiCommandDispatcher is stages 3-4 of the precedence contract. It is safe for concurrent use: the editor's completion queries run off the loop.
type TuiCommandDispatcher struct {
	options TuiCommandDispatcherOptions

	mu         sync.RWMutex
	table      *p2runtime.KeyedTable[TuiCommand]
	user       pitui.KeybindingsConfig
	platform   prefs.KeyPlatform
	overrides  pitui.KeybindingDefinitions
	entries    []*commandEntry
	slashIndex map[string]*commandEntry
	keymap     *pitui.TUIKeybindingsManager
	conflicts  []TuiCommandConflict
	privileged prefs.PrivilegedResolution
	unknown    []string
	sources    *p2runtime.KeyedTable[TuiSlashSource]

	autocomplete *slashCompletion
}

// NewTuiCommandDispatcher builds a dispatcher with no commands.
func NewTuiCommandDispatcher(options TuiCommandDispatcherOptions) *TuiCommandDispatcher {
	d := &TuiCommandDispatcher{
		options:    options,
		user:       pitui.KeybindingsConfig{},
		platform:   prefs.DefaultKeyPlatform,
		overrides:  pitui.KeybindingDefinitions{},
		privileged: prefs.PrivilegedResolution{Keys: map[string][]string{}},
	}
	d.autocomplete = &slashCompletion{dispatcher: d}
	d.rebuild()
	return d
}

// Autocomplete is the `/name` and argument completion fed by the registry (never the editor's fs-reading provider).
func (d *TuiCommandDispatcher) Autocomplete() pitui.AutocompleteProvider { return d.autocomplete }

// Manager is the current keymap.
func (d *TuiCommandDispatcher) Manager() *pitui.TUIKeybindingsManager {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.keymap
}

// Matches is key matching over dynamic ids.
func (d *TuiCommandDispatcher) Matches(data, id string) bool { return d.Manager().Matches(data, id) }

// GetKeys are the keys bound to id.
func (d *TuiCommandDispatcher) GetKeys(id string) []string { return d.Manager().GetKeys(id) }

// Conflicts are the rebuild's reports.
func (d *TuiCommandDispatcher) Conflicts() []TuiCommandConflict {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return slices.Clone(d.conflicts)
}

// Privileged is the launcher keys after the rebuild.
func (d *TuiCommandDispatcher) Privileged() prefs.PrivilegedResolution {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.privileged
}

// Unknown are unknown ids in user keybindings: kept (pi does), reported in /hotkeys.
func (d *TuiCommandDispatcher) Unknown() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return slices.Clone(d.unknown)
}

// SetSources sets the slash sources.
func (d *TuiCommandDispatcher) SetSources(table *p2runtime.KeyedTable[TuiSlashSource]) {
	d.mu.Lock()
	d.sources = table
	d.mu.Unlock()
}

// SlashEntryWithSource is a template or skill name with its source.
type SlashEntryWithSource struct {
	Entry  TuiSlashEntry
	Source TuiSlashSource
}

// SlashEntries are the template and skill names (not shadowing a command) with their source.
func (d *TuiCommandDispatcher) SlashEntries() []SlashEntryWithSource {
	ctx := d.options.Ctx()
	d.mu.RLock()
	sources := d.sources
	taken := make(map[string]bool, len(d.slashIndex))
	for name := range d.slashIndex {
		taken[name] = true
	}
	d.mu.RUnlock()
	var out []SlashEntryWithSource
	if sources == nil {
		return out
	}
	for _, key := range sources.Keys() {
		source, _ := sources.Get(key)
		for _, entry := range source.Entries(ctx) {
			if !taken[entry.Name] {
				out = append(out, SlashEntryWithSource{entry, source})
			}
		}
	}
	return out
}

var slashNameInText = regexp.MustCompile(`^/(\S+)`)

// Expand expands `/name args` through the source that lists name; other text (and commands) come back unchanged.
func (d *TuiCommandDispatcher) Expand(text string) (string, error) {
	m := slashNameInText.FindStringSubmatch(text)
	if m == nil {
		return text, nil
	}
	for _, item := range d.SlashEntries() {
		if item.Entry.Name != m[1] {
			continue
		}
		expanded, ok, err := item.Source.Expand(d.options.Ctx(), text)
		if err != nil || !ok {
			return text, err
		}
		return expanded, nil
	}
	return text, nil
}

// SetCommands replaces the command table.
func (d *TuiCommandDispatcher) SetCommands(table *p2runtime.KeyedTable[TuiCommand]) {
	d.mu.Lock()
	d.table = table
	d.mu.Unlock()
	d.rebuild()
}

// SetUserBindings sets the user keybindings and platform.
func (d *TuiCommandDispatcher) SetUserBindings(user pitui.KeybindingsConfig, platform prefs.KeyPlatform) {
	d.mu.Lock()
	d.user = user
	d.platform = platform
	d.mu.Unlock()
	d.rebuild()
}

// SetOverrides sets runtime overrides of built-in definitions (e.g. keys the current layout handles itself).
func (d *TuiCommandDispatcher) SetOverrides(overrides pitui.KeybindingDefinitions) {
	d.mu.Lock()
	d.overrides = overrides
	d.mu.Unlock()
	d.rebuild()
}

func (d *TuiCommandDispatcher) rebuild() {
	d.mu.Lock()
	d.rebuildLocked()
	d.mu.Unlock()
	if d.options.OnRebuild != nil {
		d.options.OnRebuild()
	}
}

// rebuildLocked builds a fresh keymap (section 6.3 "Keymap rebuild"), the dispatch order and the reports.
func (d *TuiCommandDispatcher) rebuildLocked() {
	builtin := prefs.BuiltinKeybindings(d.platform)
	for id, def := range d.overrides {
		builtin[id] = def
	}
	var conflicts []TuiCommandConflict
	d.privileged = prefs.ResolvePrivilegedKeys(builtin, d.user)
	for _, lost := range d.privileged.Lost {
		conflicts = append(conflicts, TuiCommandConflict{ConflictPrivileged, fmt.Sprintf("%s: %s takes it from launcher action %s", lost.Key, lost.To, lost.ID)})
	}
	privilegedKeys := map[string]bool{}
	for _, keys := range d.privileged.Keys {
		for _, key := range keys {
			privilegedKeys[key] = true
		}
	}
	definitions := make(pitui.KeybindingDefinitions, len(builtin))
	for id, def := range builtin {
		definitions[id] = def
	}
	type accepted struct {
		id      string
		command TuiCommand
		when    ParsedWhen
	}
	var accept []accepted
	if d.table != nil {
		for _, id := range d.table.Keys() {
			command, _ := d.table.Get(id)
			var when ParsedWhen
			err := error(nil)
			if !commandIDPattern.MatchString(id) || strings.HasPrefix(id, "tui.") || prefs.IsPrivileged(id) {
				err = fmt.Errorf("Invalid command id %s: tui.* and launcher ids are reserved", id)
			} else {
				when, err = ParseWhen(command.When)
			}
			if err != nil {
				conflicts = append(conflicts, TuiCommandConflict{ConflictInvalid, err.Error()})
				continue
			}
			if _, ok := builtin[id]; !ok {
				kept := []string{}
				for _, key := range command.Keys {
					if privilegedKeys[key] {
						conflicts = append(conflicts, TuiCommandConflict{ConflictPrivileged, fmt.Sprintf("%s: launcher-owned; dropped from %s's defaults", key, id)})
					}
				}
				for _, key := range command.Keys {
					if !privilegedKeys[key] {
						kept = append(kept, key)
					}
				}
				definitions[id] = pitui.TUIKeybindingDef{DefaultKeys: kept, Description: command.Title}
			}
			accept = append(accept, accepted{id, command, when})
		}
	}
	d.keymap = pitui.NewKeybindingsManager(definitions, d.user)
	pitui.SetKeybindings(d.keymap)
	d.unknown = nil
	userIDs := make([]string, 0, len(d.user))
	for id := range d.user {
		userIDs = append(userIDs, id)
	}
	sort.Strings(userIDs)
	for _, id := range userIDs {
		if _, ok := definitions[id]; !ok {
			d.unknown = append(d.unknown, id)
		}
	}
	transcript := map[string]string{}
	for _, id := range transcriptKeys {
		for _, key := range d.keymap.GetKeys(id) {
			transcript[key] = id
		}
	}
	d.entries = d.entries[:0:0]
	for order, a := range accept {
		_, userBound := d.user[a.id]
		d.entries = append(d.entries, &commandEntry{id: a.id, command: a.command, when: a.when, order: order, userBound: userBound})
	}
	byKey := map[string][]*commandEntry{}
	var keyOrder []string
	for _, entry := range d.entries {
		for _, key := range d.keymap.GetKeys(entry.id) {
			if !entry.when.Positive["overlay"] {
				if id, ok := transcript[key]; ok {
					conflicts = append(conflicts, TuiCommandConflict{ConflictTranscript, fmt.Sprintf("%s: %s (transcript) wins over %s", key, id, entry.id)})
				}
			}
			if _, ok := byKey[key]; !ok {
				keyOrder = append(keyOrder, key)
			}
			byKey[key] = append(byKey[key], entry)
		}
	}
	sort.SliceStable(d.entries, func(i, j int) bool {
		a, b := d.entries[i], d.entries[j]
		if a.when.Specificity != b.when.Specificity {
			return b.when.Specificity < a.when.Specificity
		}
		if a.userBound != b.userBound {
			return a.userBound
		}
		return a.order < b.order
	})
	var exclusive [][]string
	if d.options.Exclusive != nil {
		exclusive = d.options.Exclusive()
	}
	for _, key := range keyOrder {
		claimants := byKey[key]
		for i := 0; i < len(claimants); i++ {
			for j := i + 1; j < len(claimants); j++ {
				a, b := claimants[i], claimants[j]
				if a.when.Specificity != b.when.Specificity || !CanOverlap(a.when, b.when, exclusive) {
					continue
				}
				if a.userBound != b.userBound {
					continue
				}
				conflicts = append(conflicts, TuiCommandConflict{ConflictCommand, fmt.Sprintf("%s: %s and %s; %s wins (registered first)", key, a.id, b.id, a.id)})
			}
		}
	}
	d.slashIndex = map[string]*commandEntry{}
	byOrder := slices.Clone(d.entries)
	sort.SliceStable(byOrder, func(i, j int) bool { return byOrder[i].order < byOrder[j].order })
	for _, entry := range byOrder {
		if entry.command.Slash == nil || entry.command.Slash.Name == "" {
			continue
		}
		name := entry.command.Slash.Name
		if !slashNamePattern.MatchString(name) {
			conflicts = append(conflicts, TuiCommandConflict{ConflictSlash, fmt.Sprintf("/%s of %s is not a valid name", jsstring.Slice(name, 0, 40), entry.id)})
			continue
		}
		if holder, ok := d.slashIndex[name]; ok {
			conflicts = append(conflicts, TuiCommandConflict{ConflictSlash, fmt.Sprintf("/%s: %s keeps it; disabled for %s", name, holder.id, entry.id)})
			continue
		}
		entry.slash = name
		d.slashIndex[name] = entry
	}
	d.conflicts = conflicts
}

// applicable: while an overlay has focus, only commands that name it (or `overlay`) run before it (stage 3).
func applicable(entry *commandEntry, active map[string]bool) bool {
	if active["overlay"] {
		named := false
		for term := range entry.when.Positive {
			if term == "overlay" || strings.HasPrefix(term, "overlay:") {
				named = true
			}
		}
		if !named {
			return false
		}
	}
	return EvaluateWhen(entry.when.Expr, active)
}

// Dispatch is stages 3-4: the first applicable command bound to data consumes it. A nil active is the current contexts.
func (d *TuiCommandDispatcher) Dispatch(data string, active map[string]bool) bool {
	if active == nil {
		active = d.options.Contexts()
	}
	d.mu.RLock()
	var found *commandEntry
	for _, entry := range d.entries {
		if d.keymap.Matches(data, entry.id) && applicable(entry, active) {
			found = entry
			break
		}
	}
	d.mu.RUnlock()
	if found == nil {
		return false
	}
	d.execute(found, "")
	return true
}

// Run runs command id with args if its `when` holds in active (nil: the current contexts).
func (d *TuiCommandDispatcher) Run(id, args string, active map[string]bool) bool {
	if active == nil {
		active = d.options.Contexts()
	}
	d.mu.RLock()
	var found *commandEntry
	for _, entry := range d.entries {
		if entry.id == id {
			found = entry
			break
		}
	}
	d.mu.RUnlock()
	if found == nil || !EvaluateWhen(found.when.Expr, active) {
		return false
	}
	d.execute(found, args)
	return true
}

var slashText = regexp.MustCompile(`^/(\S+)(?:\s+([\s\S]*))?$`)

// RunSlash runs `/name args` through the slash index.
func (d *TuiCommandDispatcher) RunSlash(text string, active map[string]bool) bool {
	if active == nil {
		active = d.options.Contexts()
	}
	m := slashText.FindStringSubmatch(jsTrim(text))
	var found *commandEntry
	d.mu.RLock()
	if m != nil {
		found = d.slashIndex[m[1]]
	}
	d.mu.RUnlock()
	if found == nil || !EvaluateWhen(found.when.Expr, active) {
		return false
	}
	d.execute(found, jsTrim(m[2]))
	return true
}

func jsTrim(text string) string {
	return strings.Trim(text, " \t\n\v\f\r\u00a0\u2028\u2029\ufeff")
}

func (d *TuiCommandDispatcher) execute(entry *commandEntry, args string) {
	if entry.command.Run == nil {
		return
	}
	if err := entry.command.Run(d.options.Ctx(), args); err != nil && d.options.OnError != nil {
		d.options.OnError(err)
	}
}

// List is the commands applicable in active (all when nil), in registration order, for the palette, slash completion and /hotkeys.
func (d *TuiCommandDispatcher) List(active map[string]bool) []TuiCommandListing {
	d.mu.RLock()
	defer d.mu.RUnlock()
	byOrder := slices.Clone(d.entries)
	sort.SliceStable(byOrder, func(i, j int) bool { return byOrder[i].order < byOrder[j].order })
	out := []TuiCommandListing{}
	for _, entry := range byOrder {
		if active != nil && !EvaluateWhen(entry.when.Expr, active) {
			continue
		}
		out = append(out, TuiCommandListing{ID: entry.id, Command: entry.command, Keys: d.keymap.GetKeys(entry.id), Slash: entry.slash})
	}
	return out
}

// SlashEntry is the command with an enabled slash name.
func (d *TuiCommandDispatcher) SlashEntry(name string) (TuiCommandListing, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	entry, ok := d.slashIndex[name]
	if !ok {
		return TuiCommandListing{}, false
	}
	return TuiCommandListing{ID: entry.id, Command: entry.command}, true
}

// Contexts is the active context set right now.
func (d *TuiCommandDispatcher) Contexts() map[string]bool { return d.options.Contexts() }

// Context is the runtime's context.
func (d *TuiCommandDispatcher) Context() TuiContext { return d.options.Ctx() }

// slashCompletion is `/name` and argument completion fed by the registry (never the editor's fs-reading provider).
type slashCompletion struct{ dispatcher *TuiCommandDispatcher }

func (c *slashCompletion) GetSuggestions(ctx context.Context, lines []string, cursorLine, cursorCol int, options pitui.AutocompleteSuggestionOptions) *pitui.AutocompleteSuggestions {
	line := ""
	if cursorLine < len(lines) {
		line = lines[cursorLine]
	}
	before := jsstring.Slice(line, 0, cursorCol)
	if cursorLine != 0 || !strings.HasPrefix(before, "/") {
		return nil
	}
	space := -1
	if i := strings.Index(before, " "); i >= 0 {
		space = jsstring.Length(before[:i])
	}
	// Tab (forced) completes arguments only; command names complete while typing.
	if options.Force && space == -1 {
		return nil
	}
	active := c.dispatcher.Contexts()
	if space == -1 {
		var items []pitui.AutocompleteItem
		for _, item := range c.dispatcher.List(active) {
			if item.Slash == "" {
				continue
			}
			description := item.Command.Title
			if item.Command.Slash != nil && item.Command.Slash.ArgumentHint != "" {
				description = item.Command.Slash.ArgumentHint + " — " + item.Command.Title
			}
			items = append(items, pitui.AutocompleteItem{Value: item.Slash, Label: item.Slash, Description: description})
		}
		// Templates and skills after the commands; pi matches `skill:` names by their bare name first.
		var extra []pitui.AutocompleteItem
		for _, item := range c.dispatcher.SlashEntries() {
			entry := item.Entry
			description := entry.Description
			if entry.ArgumentHint != "" {
				description = entry.ArgumentHint + " — " + entry.Description
			}
			extra = append(extra, pitui.AutocompleteItem{Value: entry.Name, Label: entry.Name, Description: description})
		}
		query := jsstring.Slice(before, 1)
		bare := pitui.FuzzyFilter(extra, query, func(item pitui.AutocompleteItem) string {
			return strings.TrimPrefix(item.Value, "skill:")
		})
		var skills []pitui.AutocompleteItem
		for _, item := range extra {
			if strings.HasPrefix(item.Value, "skill:") && !slices.Contains(bare, item) {
				skills = append(skills, item)
			}
		}
		full := pitui.FuzzyFilter(skills, query, func(item pitui.AutocompleteItem) string { return item.Value })
		filtered := append(append(pitui.FuzzyFilter(items, query, func(item pitui.AutocompleteItem) string { return item.Value }), bare...), full...)
		if len(filtered) == 0 {
			return nil
		}
		return &pitui.AutocompleteSuggestions{Items: filtered, Prefix: before}
	}
	entry, ok := c.dispatcher.SlashEntry(jsstring.Slice(before, 1, space))
	if !ok || entry.Command.Slash == nil || entry.Command.Slash.Complete == nil || (entry.Command.Slash.TabOnly && !options.Force) {
		return nil
	}
	argument := jsstring.Slice(before, space+1)
	items, err := entry.Command.Slash.Complete(argument, c.dispatcher.Context())
	if err != nil || len(items) == 0 || ctx.Err() != nil {
		return nil
	}
	return &pitui.AutocompleteSuggestions{Items: items, Prefix: argument}
}

func (c *slashCompletion) ApplyCompletion(lines []string, cursorLine, cursorCol int, item pitui.AutocompleteItem, prefix string) ([]string, int, int) {
	line := ""
	if cursorLine < len(lines) {
		line = lines[cursorLine]
	}
	before := jsstring.Slice(line, 0, cursorCol-jsstring.Length(prefix))
	after := jsstring.Slice(line, cursorCol)
	next := slices.Clone(lines)
	if strings.HasPrefix(prefix, "/") && strings.TrimSpace(before) == "" {
		next[cursorLine] = before + "/" + item.Value + " " + after
		return next, cursorLine, jsstring.Length(before) + jsstring.Length(item.Value) + 2
	}
	next[cursorLine] = before + item.Value + after
	return next, cursorLine, jsstring.Length(before) + jsstring.Length(item.Value)
}
