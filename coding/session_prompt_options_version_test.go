package coding

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// populatedSystemPromptOptions has every field of the options set, with each collection holding an entry a test can edit in place.
func populatedSystemPromptOptions() extension.BuildSystemPromptOptions {
	return extension.BuildSystemPromptOptions{
		CustomPrompt:       "custom",
		CustomPromptSet:    true,
		ForceSystemPrompt:  new("forced"),
		SelectedTools:      []string{"read", "bash"},
		HiddenTools:        []string{"hidden"},
		ToolSnippets:       map[string]string{"read": "reads"},
		ToolGuidelines:     map[string][]string{"read": {"be careful"}},
		PromptGuidelines:   []string{"guide"},
		AppendSystemPrompt: "append",
		Sections:           &ai.OrderedSections{{Name: "plan", Value: new("plan text")}},
		Cwd:                "/work",
		ContextFiles:       []extension.SystemPromptContextFile{{Path: "AGENTS.md", Content: "rules"}},
		Skills:             []extension.SystemPromptSkill{{Name: "skill", Description: "does", FilePath: "/s/SKILL.md", BaseDir: "/s", SourceInfo: extension.SourceInfo{Path: "/s", Source: "local", Scope: "user", Origin: "top-level"}}},
	}
}

// An in-process extension holds the live options object (Pi's getSystemPromptOptions returns it, and the mutable shape is what Pi exposes), so a write to any part of it, in place, must change the version the host derives its encoding and its run prompt from. The table has one write per field of the options, and the test fails for a field it lacks: a field the version ignores would serve a stale encoding.
func TestSystemPromptOptionsVersionSeesEveryWrite(t *testing.T) {
	writes := map[string]func(o *extension.BuildSystemPromptOptions){
		"CustomPrompt":       func(o *extension.BuildSystemPromptOptions) { o.CustomPrompt += "!" },
		"CustomPromptSet":    func(o *extension.BuildSystemPromptOptions) { o.CustomPromptSet = false },
		"ForceSystemPrompt":  func(o *extension.BuildSystemPromptOptions) { *o.ForceSystemPrompt = "forced!" },
		"SelectedTools":      func(o *extension.BuildSystemPromptOptions) { o.SelectedTools[0] = "write" },
		"HiddenTools":        func(o *extension.BuildSystemPromptOptions) { o.HiddenTools[0] = "other" },
		"ToolSnippets":       func(o *extension.BuildSystemPromptOptions) { o.ToolSnippets["read"] = "reads!" },
		"ToolGuidelines":     func(o *extension.BuildSystemPromptOptions) { o.ToolGuidelines["read"][0] = "careless" },
		"PromptGuidelines":   func(o *extension.BuildSystemPromptOptions) { o.PromptGuidelines[0] = "guide!" },
		"AppendSystemPrompt": func(o *extension.BuildSystemPromptOptions) { o.AppendSystemPrompt += "!" },
		"Sections":           func(o *extension.BuildSystemPromptOptions) { *(*o.Sections)[0].Value = "plan text!" },
		"Cwd":                func(o *extension.BuildSystemPromptOptions) { o.Cwd += "/sub" },
		"ContextFiles":       func(o *extension.BuildSystemPromptOptions) { o.ContextFiles[0].Content = "rules!" },
		"Skills":             func(o *extension.BuildSystemPromptOptions) { o.Skills[0].SourceInfo.Path = "/elsewhere" },
	}
	typ := reflect.TypeFor[extension.BuildSystemPromptOptions]()
	populated := reflect.ValueOf(populatedSystemPromptOptions())
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		if populated.Field(i).IsZero() {
			t.Errorf("populatedSystemPromptOptions leaves %s unset", name)
		}
		if writes[name] == nil {
			t.Errorf("no in-place write of %s below: add it, and handle the field in copySystemPromptOptions and sameSystemPromptOptions", name)
		}
	}
	if len(writes) != typ.NumField() {
		t.Errorf("%d writes for %d fields", len(writes), typ.NumField())
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			s := &Session{}
			options := populatedSystemPromptOptions()
			s.baseSystemPromptOptions.Store(&options)
			version := s.systemPromptOptionsVersion()
			if again := s.systemPromptOptionsVersion(); again != version {
				t.Fatalf("the version moved from %d to %d without a write", version, again)
			}
			write(&options)
			if edited := s.systemPromptOptionsVersion(); edited == version {
				t.Fatalf("an in-place write to %s kept version %d", name, version)
			}
		})
	}
}

// The version names contents: a replacement that holds what the options held keeps it, any other replacement changes it, and so does the Session having no options and then some.
func TestSystemPromptOptionsVersionFollowsTheContents(t *testing.T) {
	s := &Session{}
	none := s.systemPromptOptionsVersion()
	first := populatedSystemPromptOptions()
	s.baseSystemPromptOptions.Store(&first)
	built := s.systemPromptOptionsVersion()
	if built == none {
		t.Fatalf("the first options kept version %d", none)
	}
	same := populatedSystemPromptOptions()
	s.baseSystemPromptOptions.Store(&same)
	if got := s.systemPromptOptionsVersion(); got != built {
		t.Fatalf("a replacement with the same contents moved the version from %d to %d", built, got)
	}
	other := populatedSystemPromptOptions()
	other.SelectedTools = nil
	s.baseSystemPromptOptions.Store(&other)
	replaced := s.systemPromptOptionsVersion()
	if replaced == built {
		t.Fatalf("a replacement that selects Pi's default tools instead of the two tools kept version %d", built)
	}
	empty := populatedSystemPromptOptions()
	empty.SelectedTools = []string{}
	s.baseSystemPromptOptions.Store(&empty)
	if got := s.systemPromptOptionsVersion(); got == replaced {
		t.Fatalf("an empty selectedTools, which selects no tool, kept the version of an absent one (%d)", got)
	}
}

// Reading the version of unchanged options costs no allocation: the extension host reads it before every state push and every event.
func TestSystemPromptOptionsVersionAllocatesNothingWhenUnchanged(t *testing.T) {
	s := &Session{}
	options := populatedSystemPromptOptions()
	s.baseSystemPromptOptions.Store(&options)
	s.systemPromptOptionsVersion()
	if allocs := testing.AllocsPerRun(100, func() { s.systemPromptOptionsVersion() }); allocs != 0 {
		t.Fatalf("an unchanged version costs %.0f allocations", allocs)
	}
}

// The key the extension host reads names the base options the Session hands out: it changes with every replacement of them (the tools, the resources) and every edit of the object GetSystemPromptOptions returns, and it holds while they do not change.
func TestSystemPromptOptionsKeyTracksTheBaseOptions(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true})
	s := h.session
	options := s.GetSystemPromptOptions()
	first := s.systemPromptOptionsKey()
	if again := s.GetSystemPromptOptions(); again != options || s.systemPromptOptionsKey() != first {
		t.Fatal("unchanged inputs changed the options object or its key")
	}
	active := s.ActiveToolNames()
	if len(active) < 2 {
		t.Fatalf("active tools %v: the test needs two", active)
	}

	s.SetActiveToolsByName(active[:1])
	if s.systemPromptOptionsKey() == first || s.GetSystemPromptOptions() == options || len(s.GetSystemPromptOptions().SelectedTools) != 1 {
		t.Fatalf("a tool change left the options %+v under key %v", s.GetSystemPromptOptions().SelectedTools, s.systemPromptOptionsKey())
	}
	afterTools, toolsKey := s.GetSystemPromptOptions(), s.systemPromptOptionsKey()

	s.SetSystemPromptResources(SystemPromptResources{AppendSystemPrompt: "APPENDED-BY-RESOURCES"})
	if s.systemPromptOptionsKey() == toolsKey || s.GetSystemPromptOptions() == afterTools || s.GetSystemPromptOptions().AppendSystemPrompt != "APPENDED-BY-RESOURCES" {
		t.Fatalf("a resource change left the options %+v under key %v", s.GetSystemPromptOptions(), s.systemPromptOptionsKey())
	}
	resourcesKey := s.systemPromptOptionsKey()

	// The edit an in-process command makes (agent-session-model-extension.test.ts "allows extension commands to inspect live system prompt options"): it appends to the live selectedTools.
	live := s.GetSystemPromptOptions()
	live.SelectedTools = append(live.SelectedTools, "mutated_tool")
	if s.systemPromptOptionsKey() == resourcesKey {
		t.Fatalf("an in-place edit of the options %+v left key %v", live.SelectedTools, resourcesKey)
	}
}

// A state push carries what an in-process extension wrote into the live options, as it carried it before the host kept the encoding of unchanged options: a subprocess extension's getSystemPromptOptions() answers from that push, and Pi's extensions all see one object.
func TestStatePushEncodesAnInProcessEditOfTheBaseOptions(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true})
	s := h.session
	bridge := subprocess.NewUIBridge(nil)
	BindSessionExtensionActions(nil, bridge, func() *Session { return s }, extension.ContextActions{}, nil)
	pushed := func() extension.BuildSystemPromptOptions {
		t.Helper()
		var options extension.BuildSystemPromptOptions
		if err := json.Unmarshal(bridge.Snapshot(nil, 0, false).SystemPromptOptions, &options); err != nil {
			t.Fatal(err)
		}
		return options
	}
	before := pushed()
	if again := pushed(); !slices.Equal(again.SelectedTools, before.SelectedTools) || again.AppendSystemPrompt != before.AppendSystemPrompt {
		t.Fatalf("an unchanged push read %+v, then %+v", before, again)
	}
	live := s.GetSystemPromptOptions()
	live.SelectedTools = append(live.SelectedTools, "mutated_tool")
	live.AppendSystemPrompt += "EDITED-BY-AN-EXTENSION"
	got := pushed()
	if !slices.Contains(got.SelectedTools, "mutated_tool") || !strings.HasSuffix(got.AppendSystemPrompt, "EDITED-BY-AN-EXTENSION") {
		t.Fatalf("the push after an in-place edit carries selectedTools %v and appendSystemPrompt %q", got.SelectedTools, got.AppendSystemPrompt)
	}
}

// The run's prompt follows an in-process edit of the base options. A caller-owned structured prompt that is exactly the rendering of the base options (as the print, JSON and RPC modes build it) is rebuilt whole from the run's own options; once an extension edits the base options it is not that rendering any more, and the run keeps the caller's sections (baseSectionsRenderBaseOptions).
func TestRunSystemPromptFollowsAnInProcessEditOfTheBaseOptions(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true})
	s := h.session
	base := s.GetSystemPromptOptions()
	s.toolRegistryMu.Lock()
	s.defaultSystemPrompt = false
	s.baseSystemSections = icodingagent.BeforeAgentStartRun{Options: *base}.BaseSections(s.ActiveToolNames(), s.hiddenDeclarations())
	s.toolRegistryMu.Unlock()
	runOptions := *base
	runOptions.AppendSystemPrompt = "RUN-APPENDED"
	run, err := icodingagent.ResolveBeforeAgentStartRun(*base, &extension.BeforeAgentStartCombinedResult{SystemPromptOptions: &runOptions})
	if err != nil {
		t.Fatal(err)
	}
	s.SetRunPrompt(&run)
	rebuilt := s.SystemPrompt()
	if !strings.Contains(rebuilt, "RUN-APPENDED") || rebuilt != s.SystemPrompt() {
		t.Fatalf("the run's prompt does not carry its own append text: %q", rebuilt)
	}
	base.AppendSystemPrompt += "EDITED-BY-AN-EXTENSION"
	if got := s.SystemPrompt(); got == rebuilt || strings.Contains(got, "RUN-APPENDED") {
		t.Fatalf("the prompt after an in-place edit of the base options is still the one built before it: %q", got)
	}
}
