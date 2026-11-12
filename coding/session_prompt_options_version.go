package coding

import (
	"maps"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// systemPromptOptionsWatch numbers the contents of the Session's base system prompt options. GetSystemPromptOptions hands the live object to in-process extensions, as Pi's getSystemPromptOptions returns the live _baseSystemPromptOptions, and its type is the mutable shape Pi exposes to extensions (system-prompt.ts normalizeBuildSystemPromptOptions): an extension may edit the options in place, and the next reader sees the edit. What the host derives from the options (the encoding of a state push, the run's prompt) is current only for the version it was derived from.
type systemPromptOptionsWatch struct {
	mu      sync.Mutex
	version uint64
	// seen is a copy of the options at version, sharing nothing an edit of the live object reaches; set reports that the Session had options then.
	seen extension.BuildSystemPromptOptions
	set  bool
}

// systemPromptOptionsID names one version of one Session's base options.
type systemPromptOptionsID struct {
	session *Session
	version uint64
}

// systemPromptOptionsVersion returns the version of the Session's base options: it changes whenever their contents change, whether the Session replaced the options object or an in-process extension wrote the one it was handed, and it holds while they do not. Equal contents have one version, whichever object holds them.
func (s *Session) systemPromptOptionsVersion() uint64 {
	options := s.baseSystemPromptOptions.Load()
	w := &s.systemPromptOptionsWatch
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case options == nil:
		if w.set {
			w.seen, w.set = extension.BuildSystemPromptOptions{}, false
			w.version++
		}
	case !w.set || !sameSystemPromptOptions(&w.seen, options):
		w.seen, w.set = copySystemPromptOptions(options), true
		w.version++
	}
	return w.version
}

// systemPromptOptionsKey is the key the extension host reads before a state push: the version of the base options, which it builds first when the Session has none yet.
func (s *Session) systemPromptOptionsKey() systemPromptOptionsID {
	s.GetSystemPromptOptions()
	return systemPromptOptionsID{session: s, version: s.systemPromptOptionsVersion()}
}

// copySystemPromptOptions returns a copy of options that shares nothing a write to options reaches: the collections, what the pointers refer to, and the section values are cloned, and strings are immutable. Every field of extension.BuildSystemPromptOptions is handled here and in sameSystemPromptOptions; TestSystemPromptOptionsVersionSeesEveryWrite fails for a field that is not.
func copySystemPromptOptions(options *extension.BuildSystemPromptOptions) extension.BuildSystemPromptOptions {
	c := *options
	if options.ForceSystemPrompt != nil {
		c.ForceSystemPrompt = new(*options.ForceSystemPrompt)
	}
	c.SelectedTools = slices.Clone(options.SelectedTools)
	c.HiddenTools = slices.Clone(options.HiddenTools)
	c.ToolSnippets = maps.Clone(options.ToolSnippets)
	if options.ToolGuidelines != nil {
		c.ToolGuidelines = make(map[string][]string, len(options.ToolGuidelines))
		for name, guidelines := range options.ToolGuidelines {
			c.ToolGuidelines[name] = slices.Clone(guidelines)
		}
	}
	c.PromptGuidelines = slices.Clone(options.PromptGuidelines)
	if options.Sections != nil {
		sections := cloneSystemSections(*options.Sections)
		c.Sections = &sections
	}
	c.ContextFiles = slices.Clone(options.ContextFiles)
	c.Skills = slices.Clone(options.Skills)
	return c
}

// sameSystemPromptOptions reports whether a and b hold the same contents, absent and empty collections told apart: a nil selectedTools selects Pi's default tools, and an empty one selects none. It allocates nothing, and strings of the copy share their bytes with the original, so an unchanged option costs one pointer comparison per string.
func sameSystemPromptOptions(a, b *extension.BuildSystemPromptOptions) bool {
	return a.CustomPrompt == b.CustomPrompt && a.CustomPromptSet == b.CustomPromptSet &&
		(a.ForceSystemPrompt == nil) == (b.ForceSystemPrompt == nil) && (a.ForceSystemPrompt == nil || *a.ForceSystemPrompt == *b.ForceSystemPrompt) &&
		sameElements(a.SelectedTools, b.SelectedTools) &&
		sameElements(a.HiddenTools, b.HiddenTools) &&
		(a.ToolSnippets == nil) == (b.ToolSnippets == nil) && maps.Equal(a.ToolSnippets, b.ToolSnippets) &&
		(a.ToolGuidelines == nil) == (b.ToolGuidelines == nil) && maps.EqualFunc(a.ToolGuidelines, b.ToolGuidelines, sameElements[string]) &&
		sameElements(a.PromptGuidelines, b.PromptGuidelines) &&
		a.AppendSystemPrompt == b.AppendSystemPrompt &&
		(a.Sections == nil) == (b.Sections == nil) && (a.Sections == nil || equalSections(*a.Sections, *b.Sections)) &&
		a.Cwd == b.Cwd &&
		sameElements(a.ContextFiles, b.ContextFiles) &&
		sameElements(a.Skills, b.Skills)
}

// sameElements reports whether a and b are both nil or both non-nil, with the same elements in order.
func sameElements[T comparable](a, b []T) bool {
	return (a == nil) == (b == nil) && slices.Equal(a, b)
}
