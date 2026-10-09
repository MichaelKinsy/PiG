package correspondence

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestExtractGoCurrentSettingsAndPrompts(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	commit := repositorySnapshotCommit(t, root)
	inventory, err := ExtractGo(context.Background(), root, commit)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Tables) != 1 {
		t.Fatalf("table count = %d, want 1", len(inventory.Tables))
	}
	var ids []string
	for _, item := range inventory.Tables[0].Items {
		ids = append(ids, item.ID)
		if item.ID == "image-width-cells" && !reflect.DeepEqual(item.CurrentCalls, []string{"strconv.Itoa"}) {
			t.Fatalf("image-width-cells current calls = %v", item.CurrentCalls)
		}
	}
	wantIDs := []string{
		"autocompact", "show-images", "image-width-cells", "auto-resize-images", "block-images", "skill-commands",
		"show-hardware-cursor", "editor-padding", "output-padding", "autocomplete-max-visible", "clear-on-shrink",
		"terminal-progress", "steering-mode", "follow-up-mode", "transport", "http-idle-timeout", "cache-warming-mode",
		"hide-thinking", "mermaid-rendering", "cache-miss-notices", "collapse-changelog", "quiet-startup", "install-telemetry",
		"default-project-trust", "double-escape-action", "tree-filter-mode", "mask-secret-input", "warnings", "model-thinking", "tui-mode",
		"fullscreen-exit-output", "fullscreen-scrollbar", "fullscreen-copy-on-select", "fullscreen-wheel-scroll-lines", "theme",
	}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("settings IDs = %v, want %v", ids, wantIDs)
	}
	callbacks := inventory.Tables[0].Callbacks
	if len(callbacks) != len(wantIDs) {
		t.Fatalf("callback count = %d, want %d", len(callbacks), len(wantIDs))
	}
	for _, callback := range callbacks {
		if callback.ID == "image-width-cells" {
			if len(callback.Writes) != 0 || !reflect.DeepEqual(callback.Calls, []string{"callbacks.OnImageWidthCellsChange", "settingsParseInt"}) {
				t.Fatalf("image-width-cells effects = %#v", callback)
			}
		}
	}
	productionCallbacks := inventory.Tables[0].ProductionCallbacks
	if len(productionCallbacks) != len(wantIDs) {
		t.Fatalf("production callback count = %d, want %d", len(productionCallbacks), len(wantIDs))
	}
	for _, callback := range productionCallbacks {
		if callback.ID != "autocompact" {
			continue
		}
		roles := make([]string, len(callback.Segments))
		for index, segment := range callback.Segments {
			roles[index] = segment.Role
		}
		if !reflect.DeepEqual(roles, []string{"persistence", "runtime-dispatch", "runtime-case", "runtime-common"}) || callback.Segments[2].Calls[0].Callee != "m.statusLine.SetAutoCompactEnabled" {
			t.Fatalf("autocompact production callback = %#v", callback)
		}
	}
	var promptNames []string
	for _, constant := range inventory.Constants {
		promptNames = append(promptNames, constant.Name)
	}
	wantPromptNames := []string{"SUMMARIZATION_PROMPT", "UPDATE_SUMMARIZATION_PROMPT", "turnPrefixSummarizationPrompt", "SummarizationSystemPrompt"}
	if !reflect.DeepEqual(promptNames, wantPromptNames) {
		t.Fatalf("prompt names = %v, want %v", promptNames, wantPromptNames)
	}
	if len(inventory.Functions) != 10 {
		t.Fatalf("function count = %d, want 10", len(inventory.Functions))
	}
	compact := inventory.Functions[0]
	if compact.Name != "compact" || compact.Async || !reflect.DeepEqual(compact.CancellationInputs, []string{"ctx"}) || len(compact.Callers) != 1 || !strings.HasPrefix(compact.Callers[0].Expression, "compaction.Compact") {
		t.Fatalf("compact identity = %#v", compact)
	}
	var ordered []string
	for _, call := range compact.Calls {
		if call.Callee == "GenerateSummaryWithUsageUsing" || call.Callee == "generateTurnPrefixSummary" || call.Callee == "combineUsage" {
			ordered = append(ordered, call.Callee)
		}
	}
	if !reflect.DeepEqual(ordered, []string{"GenerateSummaryWithUsageUsing", "generateTurnPrefixSummary", "combineUsage", "GenerateSummaryWithUsageUsing"}) {
		t.Fatalf("compact ordered calls = %v", ordered)
	}
	var stateProfile []string
	for _, transition := range compact.Transitions {
		if transition.Target == "historySummary" || transition.Target == "summary" || transition.Kind == "return" {
			stateProfile = append(stateProfile, transition.Kind+":"+transition.Target)
		}
	}
	wantState := []string{"bind:summary", "bind:historySummary", "update:historySummary", "return:", "return:", "update:summary", "update:summary", "return:", "update:summary", "return:", "return:"}
	if !reflect.DeepEqual(stateProfile, wantState) {
		t.Fatalf("compact state profile = %v, want %v", stateProfile, wantState)
	}
	for _, function := range inventory.Functions {
		if function.Name == "applyOverrides" {
			if len(function.Callers) != 0 {
				t.Fatalf("applyOverrides callers = %#v, want none", function.Callers)
			}
			continue
		}
		if len(function.Callers) == 0 {
			t.Fatalf("function %s has no compiled production caller", function.Name)
		}
	}
	wantManager := []string{"applyOverrides", "mergeSettings", "persistScopedSettings", "reload", "saveGlobal", "saveProject", "setProjectTrusted"}
	var manager []string
	orchestration := false
	for _, function := range inventory.Functions {
		if function.Kind == "settings-manager" {
			manager = append(manager, function.Name)
		}
		if function.Kind == "settings-orchestration" && function.Name == "settingsOrchestration" {
			orchestration = true
		}
	}
	slices.Sort(manager)
	if !reflect.DeepEqual(manager, wantManager) || !orchestration {
		t.Fatalf("settings manager functions/orchestration = %v/%t, want %v/true", manager, orchestration, wantManager)
	}
}

func TestExtractGoCacheMissNoticesDescriptionMatchesPi0861(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	commit := repositorySnapshotCommit(t, root)
	inventory, err := ExtractGo(t.Context(), root, commit)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range inventory.Tables[0].Items {
		if item.ID == "cache-miss-notices" {
			const want = "Show transcript notices for cache costs and provider recovery diagnostics"
			if item.Description != want {
				t.Fatalf("cache-miss-notices description = %q, want %q", item.Description, want)
			}
			return
		}
	}
	t.Fatal("cache-miss-notices setting not found")
}

func TestExtractGoFullscreenExitOutputMatchesPi0861(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	commit := repositorySnapshotCommit(t, root)
	inventory, err := ExtractGo(t.Context(), root, commit)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range inventory.Tables[0].Items {
		if item.ID == "fullscreen-exit-output" {
			if item.Label != "Fullscreen exit output" || item.Description != "Print the transcript or only a session resume hint when exiting fullscreen mode" || !slices.Equal(item.Values, []string{"transcript", "resume-hint"}) {
				t.Fatalf("fullscreen-exit-output = %#v", item)
			}
			return
		}
	}
	t.Fatal("fullscreen-exit-output setting not found")
}

func TestExtractGoFullscreenCopyOnSelectMatchesPi0861(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	commit := repositorySnapshotCommit(t, root)
	inventory, err := ExtractGo(t.Context(), root, commit)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range inventory.Tables[0].Items {
		if item.ID == "fullscreen-copy-on-select" {
			if item.Label != "Fullscreen copy on select" || item.Description != "Automatically copy selected text in fullscreen mode; disable to copy selections with Ctrl+X" || !slices.Equal(item.Values, []string{"true", "false"}) {
				t.Fatalf("fullscreen-copy-on-select = %#v", item)
			}
			return
		}
	}
	t.Fatal("fullscreen-copy-on-select setting not found")
}

func TestAddGoFunctionCallersRetainsAllReviewedCallSites(t *testing.T) {
	// The caller inventory pins edges, not a unique invocation per function.
	// In particular Load can merge global/overrides on the untrusted branch
	// and global/project/overrides on the trusted branch.
	for _, tc := range []struct {
		name string
		body string
		want []FunctionCaller
	}{
		{name: "missing", body: "return\n"},
		{name: "single", body: "mergeSettings(global, project)\n", want: []FunctionCaller{
			{Expression: "mergeSettings(global, project)", StartLine: 3},
		}},
		{name: "trust-branches", body: "if !trusted {\nmergeSettings(global, overrides)\nreturn\n}\nmergeSettings(merged, overrides)\n", want: []FunctionCaller{
			{Expression: "mergeSettings(global, overrides)", StartLine: 4},
			{Expression: "mergeSettings(merged, overrides)", StartLine: 7},
		}},
		{name: "nested", body: "if !trusted {\nmergeSettings(global, overrides)\nreturn\n}\nmergeSettings(mergeSettings(global, project), overrides)\n", want: []FunctionCaller{
			{Expression: "mergeSettings(global, overrides)", StartLine: 4},
			{Expression: "mergeSettings(mergeSettings(global, project), overrides)", StartLine: 7},
			{Expression: "mergeSettings(global, project)", StartLine: 7},
		}},
		{name: "identical-sites", body: "if trusted {\nmergeSettings(global, project)\n} else {\nmergeSettings(global, project)\n}\n", want: []FunctionCaller{
			{Expression: "mergeSettings(global, project)", StartLine: 4},
			{Expression: "mergeSettings(global, project)", StartLine: 6},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			command := exec.CommandContext(t.Context(), "git", "init", "-q", root)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("init source fixture: %v\n%s", err, output)
			}
			// These are parser inputs for the reviewed caller paths, not runtime stubs.
			for path, source := range map[string]string{
				"coding/session_summarization_auth.go":           "package fixture\nfunc runDefaultCompaction() { compaction.Compact() }\n",
				"internal/codingagent/compaction/compaction.go":  "package fixture\nfunc CompactUsing() { generateTurnPrefixSummary() }\n",
				"internal/codingagent/settings.go":               "package fixture\nfunc Load() {\n" + tc.body + "}\nfunc UpdateGlobal() { saveSettingsPatch() }\nfunc SetBlockImages() { sm.UpdateGlobal() }\nfunc SetProjectPackages() { sm.UpdateProject() }\n",
				"coding/cli/cli_runtime_build.go":                "package fixture\ntype cliRuntimeBuilder struct{}\nfunc (b *cliRuntimeBuilder) buildResources() { services.SettingsManager().SetProjectTrusted(true) }\n",
				"internal/codingagent/interactive_commands.go":   "package fixture\nfunc buildSlashContext() { m.opts.SettingsManager.Reload() }\n",
				"internal/codingagent/slash_session_handlers.go": "package fixture\nfunc settingsHandlerTUI() {}\nfunc settingsHandler() { settingsHandlerTUI() }\n",
			} {
				path = filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			commit := repositorySnapshotCommit(t, root)
			functions := []Function{{Name: "compact"}, {Name: "generateTurnPrefixSummary"}, {Name: "mergeSettings"}, {Name: "setProjectTrusted"}, {Name: "reload"}, {Name: "persistScopedSettings"}, {Name: "saveGlobal"}, {Name: "saveProject"}, {Name: "settingsOrchestration"}}
			err := addGoFunctionCallers(t.Context(), root, "", commit, functions)
			if len(tc.want) == 0 {
				if err == nil || !strings.Contains(err.Error(), "Load has 0 calls to mergeSettings") {
					t.Fatalf("missing caller error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := slices.Clone(tc.want)
			for i := range want {
				want[i].Path = "internal/codingagent/settings.go"
				want[i].Symbol = "Load"
				want[i].SourceHash = hashString(want[i].Expression)
			}
			if got := functions[2].Callers; !reflect.DeepEqual(got, want) {
				t.Fatalf("callsite pins = %#v, want %#v", got, want)
			}
			// resource-loader.ts:399 applies the resolved trust; PiG does so in the cliRuntimeBuilder.buildResources method.
			trustCall := "services.SettingsManager().SetProjectTrusted(true)"
			wantTrust := []FunctionCaller{{Path: "coding/cli/cli_runtime_build.go", Symbol: "buildResources", Expression: trustCall, StartLine: 3, SourceHash: hashString(trustCall)}}
			if got := functions[3].Callers; !reflect.DeepEqual(got, wantTrust) {
				t.Fatalf("setProjectTrusted callsite pins = %#v, want %#v", got, wantTrust)
			}
		})
	}
}

func TestGoSemanticEffectsDetectWritesAndCalls(t *testing.T) {
	const source = `package fixture
func apply(s *Settings, v string) {
	parsed := parse(v)
	s.Value = parsed
	if s.Enabled { notify(s.Value) }
}`
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, "fixture.go", source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	function := file.Decls[0].(*ast.FuncDecl)
	effects := goSemanticEffects(function.Body, []byte(source), files)
	if !reflect.DeepEqual(effects.Writes, []string{"parsed", "s.Value"}) || !reflect.DeepEqual(effects.Calls, []string{"notify", "parse"}) {
		t.Fatalf("effects = %#v", effects)
	}
	if !reflect.DeepEqual(effects.Reads, []string{"s.Enabled", "s.Value"}) {
		t.Fatalf("reads = %v", effects.Reads)
	}
}

// A cycle row may pass its values as typed closed-union constants, with or without the string conversion; an unknown name or a non-constant stays unresolved.
func TestGoConstantStringResolvesNamedStringConstants(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "settings.go", "package p\n\ntype Mode string\n\nconst (\n\tModeA Mode = \"a\"\n\tModeB Mode = \"b\"\n)\n\nvar notConstant = \"x\"\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	constants := goStringConstants(file)
	for source, want := range map[string]string{"string(ModeA)": "a", "ModeB": "b"} {
		expression, err := parser.ParseExpr(source)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := goConstantString(expression, constants); !ok || got != want {
			t.Fatalf("%s = %q, %v; want %q", source, got, ok, want)
		}
	}
	for _, source := range []string{"string(Missing)", "notConstant", `"literal"`, "f(ModeA)"} {
		expression, err := parser.ParseExpr(source)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := goConstantString(expression, constants); ok {
			t.Fatalf("%s resolved to %q", source, got)
		}
	}
}
