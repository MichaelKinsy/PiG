package coding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi 0.87.1 agent-session.ts:1371-1394 (_rebuildSystemPrompt) builds the base options from the tool registry and the resource loader. The expected values below were observed from the Pi oracle running before_agent_start with `--append-system-prompt APPENDED` and one AGENTS.md in the working directory.
func TestSessionSystemPromptOptionsCarryRegistryAndResources(t *testing.T) {
	resources := &SystemPromptResources{
		AppendSystemPrompt: "APPENDED",
		ContextFiles:       []extension.SystemPromptContextFile{{Path: "/work/AGENTS.md", Content: "project rules here\n"}},
		Skills:             []extension.SystemPromptSkill{{Name: "review", Description: "Review code", FilePath: "/skills/review/SKILL.md", BaseDir: "/skills/review"}},
	}
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true, resources: resources})
	options := h.session.GetSystemPromptOptions()

	if options.CustomPrompt != "" {
		t.Errorf("customPrompt = %q; Pi leaves it undefined without a SYSTEM.md or --system-prompt", options.CustomPrompt)
	}
	if options.AppendSystemPrompt != "APPENDED" {
		t.Errorf("appendSystemPrompt = %q", options.AppendSystemPrompt)
	}
	if !reflect.DeepEqual(options.ContextFiles, resources.ContextFiles) {
		t.Errorf("contextFiles = %+v", options.ContextFiles)
	}
	if !reflect.DeepEqual(options.Skills, resources.Skills) {
		t.Errorf("skills = %+v", options.Skills)
	}
	if want := h.session.ActiveToolNames(); !reflect.DeepEqual(options.SelectedTools, want) {
		t.Errorf("selectedTools = %v want %v", options.SelectedTools, want)
	}
	for name, want := range map[string]string{
		"read":  "Read file contents",
		"bash":  "Execute bash commands (ls, grep, find, etc.)",
		"edit":  "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
		"write": "Create or overwrite files",
		"grep":  "Search file contents for patterns (respects .gitignore)",
		"find":  "Find files by glob pattern (respects .gitignore)",
		"ls":    "List directory contents",
	} {
		if got := options.ToolSnippets[name]; got != want {
			t.Errorf("toolSnippets[%s] = %q want %q", name, got, want)
		}
	}
	wantGuidelines := map[string][]string{
		"read":  {"Use read to examine files instead of cat or sed."},
		"write": {"Use write only for new files or complete rewrites."},
		"edit": {
			"Use edit for precise changes (edits[].oldText must match exactly)",
			"When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls",
			"Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.",
			"Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.",
		},
	}
	for name, want := range wantGuidelines {
		if got := options.ToolGuidelines[name]; !reflect.DeepEqual(got, want) {
			t.Errorf("toolGuidelines[%s] = %q want %q", name, got, want)
		}
	}
	// Pi keeps a tool without guidelines out of toolGuidelines (agent-session.ts:3186-3192).
	for name, got := range options.ToolGuidelines {
		if len(got) == 0 {
			t.Errorf("toolGuidelines[%s] is present but empty", name)
		}
	}
}

// A default Session reports no custom prompt and includes registry metadata.
func TestSessionSystemPromptOptionsWithoutResourcesUseRegistry(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true})
	if got := h.session.GetSystemPromptOptions(); got.CustomPrompt != "" || len(got.ToolSnippets) == 0 {
		t.Fatalf("default prompt options = %+v", got)
	}
}

// Pi's rebuild replaces the resource state; reloaded skills reach the next before_agent_start.
func TestSessionSetSystemPromptResourcesUpdatesBaseOptions(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true, resources: &SystemPromptResources{CustomPrompt: "custom"}})
	if got := h.session.GetSystemPromptOptions().CustomPrompt; got != "custom" {
		t.Fatalf("customPrompt = %q", got)
	}
	h.session.SetSystemPromptResources(SystemPromptResources{CustomPrompt: "custom", Skills: []extension.SystemPromptSkill{{Name: "s", FilePath: "/s"}}})
	if got := h.session.GetSystemPromptOptions().Skills; len(got) != 1 || got[0].Name != "s" {
		t.Fatalf("skills = %+v", got)
	}
}

// Caller level: a Node extension over the production subprocess wire receives Pi's option content in before_agent_start. forceSystemPrompt reaches a later handler after an earlier one returns systemPrompt (runner.ts:1346-1348).
func TestSessionBeforeAgentStartReceivesPiOptionContent(t *testing.T) {
	for _, forced := range []string{"FORCED", ""} {
		t.Run("force="+forced, func(t *testing.T) {
			testSessionBeforeAgentStartOptionContent(t, forced)
		})
	}
}

func testSessionBeforeAgentStartOptionContent(t *testing.T, forced string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "options.json")
	source := `import {writeFileSync} from "node:fs";
export default function(pi){
  pi.on("before_agent_start", event => ({systemPrompt: ` + strconv.Quote(forced) + `}));
  pi.on("before_agent_start", (event, ctx) => { writeFileSync(` + strconv.Quote(out) + `, JSON.stringify({event: event.systemPromptOptions})); });
}`
	ext := context9789Node(t, source)
	resources := &SystemPromptResources{
		AppendSystemPrompt: "APPENDED",
		ContextFiles:       []extension.SystemPromptContextFile{{Path: "/work/AGENTS.md", Content: "project rules here\n"}},
		Skills:             []extension.SystemPromptSkill{{Name: "review", Description: "Review code", FilePath: "/s/review/SKILL.md", BaseDir: "/s/review", SourceInfo: map[string]any{"scope": "project"}}},
	}
	h := newRecoveryHarness(t, harnessOptions{defaultTools: true, extension: ext, resources: resources}, fauxReply("ok", ai.StopReasonStop, 0))
	context9789Prompt(t, h, "hello")
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Event map[string]json.RawMessage }
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	str := func(key string) string {
		var v string
		_ = json.Unmarshal(got.Event[key], &v)
		return v
	}
	if str("appendSystemPrompt") != "APPENDED" || str("forceSystemPrompt") != forced || got.Event["forceSystemPrompt"] == nil {
		t.Errorf("append=%q force=%q", str("appendSystemPrompt"), str("forceSystemPrompt"))
	}
	var files []extension.SystemPromptContextFile
	_ = json.Unmarshal(got.Event["contextFiles"], &files)
	if !reflect.DeepEqual(files, resources.ContextFiles) {
		t.Errorf("contextFiles = %s", got.Event["contextFiles"])
	}
	var skills []map[string]any
	_ = json.Unmarshal(got.Event["skills"], &skills)
	if len(skills) != 1 || skills[0]["baseDir"] != "/s/review" || skills[0]["disableModelInvocation"] != false || skills[0]["sourceInfo"] == nil {
		t.Errorf("skills = %s", got.Event["skills"])
	}
	var snippets map[string]string
	_ = json.Unmarshal(got.Event["toolSnippets"], &snippets)
	if snippets["read"] != "Read file contents" || len(snippets) < 7 {
		t.Errorf("toolSnippets = %s", got.Event["toolSnippets"])
	}
	var guidelines map[string][]string
	_ = json.Unmarshal(got.Event["toolGuidelines"], &guidelines)
	if len(guidelines["edit"]) != 4 {
		t.Errorf("toolGuidelines = %s", got.Event["toolGuidelines"])
	}
	if _, present := got.Event["customPrompt"]; present {
		t.Errorf("customPrompt present: %s", got.Event["customPrompt"])
	}
}
