// Ports Pi 1.2.0 packages/durable/test/tool-search.test.ts.

package toolsearch

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

var testContext = context.Background()

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

// chat is chat-support.ts ChatSetup: the models and registry that survive a close and reopen.
type chat struct {
	faux     *ai.FauxProviderHandle
	registry harness.Registry
	models   *ai.Models
}

func chatSetup() *chat {
	faux := ai.NewFauxProvider(ai.FauxConfig{})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	return &chat{faux: faux, models: models, registry: harness.CreateRegistry()}
}

func (c *chat) open(t *testing.T) (harness.Harness, harness.Conversation) {
	t.Helper()
	storage, err := sqlitenode.OpenNodeSqliteStorage(":memory:", sqlitenode.NodeSqliteStorageOptions{})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := harness.OpenHarness(testContext, storage, harness.HarnessOptions{Models: c.models, Registry: c.registry})
	if err != nil {
		t.Fatal(err)
	}
	root, err := opened.Root(testContext, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close(testContext) })
	return opened, root
}

func allEntries(t *testing.T, conversation harness.Conversation) []durable.EntryRecord {
	t.Helper()
	page, err := conversation.Entries(testContext, durable.EntryQuery{}, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]durable.EntryRecord, 0, len(page.Items))
	for _, item := range slices.Backward(page.Items) {
		entries = append(entries, item)
	}
	return entries
}

func addTool(t *testing.T, registry harness.Registry, tool *durable.ToolRegistration) {
	t.Helper()
	if err := registry.Install(&durable.Extension{Name: "tool:" + tool.Name, Tools: []*durable.ToolRegistration{tool}}); err != nil {
		t.Fatal(err)
	}
}

func tool(name, description string, deferred bool) *durable.ToolRegistration {
	var parameters map[string]any
	if err := json.Unmarshal([]byte(`{"type":"object","properties":{"number":{"type":"number","description":"Issue number"}}}`), &parameters); err != nil {
		panic(err)
	}
	return harness.DefineTool(durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: name, Description: description, Parameters: parameters},
		Deferred:   deferred,
		Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: name + " ran"}}}, nil
		},
	})
}

func done() ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}})
}

func call(name string, args map[string]any, id string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall(name, args, &ai.FauxToolCallOptions{ID: id})}, StopReason: "toolUse"})
}

// setup: `alpha`, the deferred `github_get_issue` and `github_list_repos`, then `omega`, and `tool_search` last.
func setup(t *testing.T) *chat {
	t.Helper()
	c := chatSetup()
	addTool(t, c.registry, tool("alpha", "Alpha tool", false))
	addTool(t, c.registry, tool("github_get_issue", "Get a GitHub issue by number", true))
	addTool(t, c.registry, tool("github_list_repos", "List the repositories of a user", true))
	addTool(t, c.registry, tool("omega", "Omega tool", false))
	if err := c.registry.Install(CreateExtension()); err != nil {
		t.Fatal(err)
	}
	return c
}

func names(tools []*durable.ToolRegistration) []string {
	out := []string{}
	for _, each := range tools {
		out = append(out, each.Name)
	}
	return out
}

func offered(t *testing.T, root harness.Conversation) []string {
	t.Helper()
	return names(must(root.Agent(testContext)).Tools)
}

func deferredNames(t *testing.T, root harness.Conversation) []string {
	t.Helper()
	return names(must(root.Agent(testContext)).Deferred)
}

func run(t *testing.T, root harness.Conversation, content string) {
	t.Helper()
	submission := must(root.Submit(testContext, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText(content)}))
	if _, err := submission.Wait(testContext); err != nil {
		t.Fatal(err)
	}
}

func modelTools(t *testing.T, opened harness.Harness, root harness.Conversation) *harness.ToolSelection {
	t.Helper()
	state, err := durable.Snapshot[harness.AgentState](testContext, opened, harness.AgentDoc, root.Id())
	if err != nil {
		t.Fatal(err)
	}
	if state == nil {
		return nil
	}
	return state.ModelTools
}

// systemMessages are the model messages of the pi.system entries, oldest first.
func systemMessages(entries []durable.EntryRecord) []ai.SystemMessage {
	var out []ai.SystemMessage
	for i := range entries {
		if durable.SystemEntry.Is(&entries[i]) {
			out = append(out, entries[i].Model[0].(ai.SystemMessage))
		}
	}
	return out
}

func addedNames(message ai.SystemMessage) []string {
	out := []string{}
	for _, each := range message.ToolsAdded {
		out = append(out, each.Name)
	}
	return out
}

func toolResultTexts(entries []durable.EntryRecord) []string {
	var out []string
	for i := range entries {
		if durable.ToolResultEntry.Is(&entries[i]) {
			var text strings.Builder
			for _, item := range entries[i].Model[0].(ai.ToolResultMessage).Content {
				if part, ok := item.(ai.TextContent); ok {
					text.WriteString(part.Text)
				}
			}
			out = append(out, text.String())
		}
	}
	return out
}

func equal(t *testing.T, label string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

// Pi 1.2.0 packages/durable/test/tool-search.test.ts:63 "offers a deferred tool only once tool_search loads it, by appending its declaration"
func TestOffersADeferredToolOnlyOnceToolSearchLoadsItByAppendingItsDeclaration(t *testing.T) {
	c := setup(t)
	c.faux.SetResponses([]ai.FauxResponseStep{
		call("tool_search", map[string]any{"query": "github issue", "limit": 1}, "c1"),
		call("github_get_issue", map[string]any{"number": 42}, "c2"),
		done(),
	})
	opened, root := c.open(t)
	equal(t, "offered", offered(t, root), []string{"alpha", "omega", "tool_search"})
	equal(t, "deferred", deferredNames(t, root), []string{"github_get_issue", "github_list_repos"})
	// Deferred tools stay callable by other tools, such as code mode scripts.
	if !slices.Contains(names(must(root.Agent(testContext)).Callable), "github_get_issue") {
		t.Error("a deferred tool is not callable by tools")
	}
	run(t, root, "go")
	entries := allEntries(t, root)
	results := toolResultTexts(entries)
	if len(results) != 2 || results[0] != "Loaded 1 tool. They are available from your next call:\n- github_get_issue: Get a GitHub issue by number" || results[1] != "github_get_issue ran" {
		t.Fatalf("results = %q", results)
	}
	if got := modelTools(t, opened, root); got == nil || got.Exact || got.Remove != nil || !slices.Equal(got.Add, []string{"github_get_issue"}) {
		t.Errorf("modelTools = %+v, want {add: [github_get_issue]}", got)
	}
	equal(t, "offered after the load", offered(t, root), []string{"alpha", "omega", "tool_search", "github_get_issue"})
	// A later search sees only the tools not loaded yet.
	equal(t, "deferred after the load", deferredNames(t, root), []string{"github_list_repos"})
	// The load appended the declaration: no tool was removed and declared again, so the cached prefix holds.
	system := systemMessages(entries)
	if len(system) < 2 {
		t.Fatalf("system messages = %d", len(system))
	}
	equal(t, "baseline", addedNames(system[0]), []string{"alpha", "omega", "tool_search"})
	equal(t, "load", addedNames(system[1]), []string{"github_get_issue"})
	if len(system[1].ToolsRemoved) != 0 {
		t.Errorf("the load removed %v", system[1].ToolsRemoved)
	}
}

// Pi 1.2.0 packages/durable/test/tool-search.test.ts:99 "appends each loaded tool in load order, not registry order"
func TestAppendsEachLoadedToolInLoadOrderNotRegistryOrder(t *testing.T) {
	c := setup(t)
	c.faux.SetResponses([]ai.FauxResponseStep{
		call("tool_search", map[string]any{"query": "repositories", "limit": 1}, "c1"),
		call("tool_search", map[string]any{"query": "issue", "limit": 1}, "c2"),
		done(),
	})
	_, root := c.open(t)
	run(t, root, "go")
	equal(t, "offered", offered(t, root), []string{"alpha", "omega", "tool_search", "github_list_repos", "github_get_issue"})
	loads := systemMessages(allEntries(t, root))[1:]
	if len(loads) != 2 {
		t.Fatalf("loads = %d", len(loads))
	}
	equal(t, "first load", addedNames(loads[0]), []string{"github_list_repos"})
	equal(t, "second load", addedNames(loads[1]), []string{"github_get_issue"})
	for _, load := range loads {
		if len(load.ToolsRemoved) != 0 {
			t.Errorf("a load removed %v", load.ToolsRemoved)
		}
	}
}

// Pi 1.2.0 packages/durable/test/tool-search.test.ts:118 "records loads by name: unknown and non-deferred names change nothing, remove wins, and a load lifts it"
func TestRecordsLoadsByNameUnknownAndNonDeferredNamesChangeNothingRemoveWinsAndALoadLiftsIt(t *testing.T) {
	c := setup(t)
	grow := harness.DefineTool(durable.ToolRegistration{
		ToolSchema: ai.ToolSchema{Name: "grow", Description: "grow", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}},
		Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{}, Control: &durable.ToolControl{AddTools: []string{"github_get_issue", "alpha", "ghost"}}}, nil
		},
	})
	addTool(t, c.registry, grow)
	c.faux.SetResponses([]ai.FauxResponseStep{call("grow", map[string]any{}, "c1"), done(), call("grow", map[string]any{}, "c2"), done()})
	opened, root := c.open(t)
	run(t, root, "go")
	want := []string{"github_get_issue", "alpha", "ghost"}
	if got := modelTools(t, opened, root); got == nil || got.Exact || got.Remove != nil || !slices.Equal(got.Add, want) {
		t.Fatalf("modelTools = %+v, want add %v", got, want)
	}
	equal(t, "offered", offered(t, root), []string{"alpha", "omega", "tool_search", "grow", "github_get_issue"})
	// A name in both remove and add is not offered.
	var getIssue *durable.ToolRegistration
	for _, each := range must(root.Agent(testContext)).Callable {
		if each.Name == "github_get_issue" {
			getIssue = each
		}
	}
	if err := root.Configure(testContext, harness.AgentChange{ModelTools: harness.SetTo(harness.ToolChange{Edit: &harness.ToolEdit{Remove: []*durable.ToolRegistration{getIssue}, Add: []*durable.ToolRegistration{getIssue}}})}); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(offered(t, root), "github_get_issue") {
		t.Error("a tool in both remove and add is offered")
	}
	// Loading it again drops it from remove.
	run(t, root, "again")
	if got := modelTools(t, opened, root); got == nil || got.Exact || got.Remove != nil || !slices.Equal(got.Add, want) {
		t.Fatalf("modelTools = %+v, want add %v and no remove", got, want)
	}
	if !slices.Contains(offered(t, root), "github_get_issue") {
		t.Error("a loaded tool is not offered")
	}
}

// Pi 1.2.0 packages/durable/test/tool-search.test.ts:147 "keeps a loaded tool when its extension is gone for a while, and appends to an explicit array"
func TestKeepsALoadedToolWhenItsExtensionIsGoneForAWhileAndAppendsToAnExplicitArray(t *testing.T) {
	c := setup(t)
	issue := tool("github_get_issue", "Get a GitHub issue by number", true)
	c.faux.SetResponses([]ai.FauxResponseStep{call("tool_search", map[string]any{"query": "github issue", "limit": 1}, "c1"), done()})
	opened, root := c.open(t)
	run(t, root, "go")
	// The server goes away: its extension is installed again without the tool.
	if err := c.registry.Install(&durable.Extension{Name: "tool:github_get_issue", Tools: []*durable.ToolRegistration{}}); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(offered(t, root), "github_get_issue") {
		t.Error("a tool of an extension without it is offered")
	}
	if err := c.registry.Install(&durable.Extension{Name: "tool:github_get_issue", Tools: []*durable.ToolRegistration{issue}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(offered(t, root), "github_get_issue") {
		t.Error("a loaded tool is not offered again once its extension is back")
	}
	// With an explicit array, a load appends to it as before.
	var alpha, search *durable.ToolRegistration
	for _, each := range must(root.Agent(testContext)).Tools {
		switch each.Name {
		case "alpha":
			alpha = each
		case "tool_search":
			search = each
		}
	}
	if err := root.Configure(testContext, harness.AgentChange{ModelTools: harness.SetTo(harness.ToolChange{List: []*durable.ToolRegistration{alpha, search}})}); err != nil {
		t.Fatal(err)
	}
	c.faux.SetResponses([]ai.FauxResponseStep{call("tool_search", map[string]any{"query": "repositories", "limit": 1}, "c2"), done()})
	run(t, root, "more")
	if got := modelTools(t, opened, root); got == nil || !got.Exact || !slices.Equal(got.Names, []string{"alpha", "tool_search", "github_list_repos"}) {
		t.Errorf("modelTools = %+v", got)
	}
}

// Pi 1.2.0 packages/durable/test/tool-search.test.ts:172 "keeps loaded tools per branch and in the baseline after a reset"
func TestKeepsLoadedToolsPerBranchAndInTheBaselineAfterAReset(t *testing.T) {
	c := setup(t)
	c.faux.SetResponses([]ai.FauxResponseStep{call("tool_search", map[string]any{"query": "github issue", "limit": 1}, "c1"), done()})
	_, root := c.open(t)
	run(t, root, "go")
	entries := allEntries(t, root)
	ownerless := harness.ConversationCreateOptions{Ownership: durable.ConversationOwnership{Kind: durable.ConversationOwnerless}}
	before := must(root.Fork(testContext, entries[0].Id, ownerless))
	after := must(root.Fork(testContext, entries[len(entries)-1].Id, ownerless))
	if slices.Contains(offered(t, before), "github_get_issue") {
		t.Error("a fork before the load offers the loaded tool")
	}
	if !slices.Contains(offered(t, after), "github_get_issue") {
		t.Error("a fork after the load does not offer the loaded tool")
	}
	if err := root.Reset(testContext, nil); err != nil {
		t.Fatal(err)
	}
	c.faux.SetResponses([]ai.FauxResponseStep{done()})
	run(t, root, "fresh")
	system := systemMessages(allEntries(t, root))
	equal(t, "baseline", addedNames(system[len(system)-1]), []string{"alpha", "omega", "tool_search", "github_get_issue"})
}

// Pi 1.2.0 packages/durable/test/tool-search.test.ts:195 "finds nothing for an unrelated query and loads nothing"
func TestFindsNothingForAnUnrelatedQueryAndLoadsNothing(t *testing.T) {
	c := setup(t)
	c.faux.SetResponses([]ai.FauxResponseStep{call("tool_search", map[string]any{"query": "weather forecast"}, "c1"), done()})
	opened, root := c.open(t)
	run(t, root, "go")
	if got := toolResultTexts(allEntries(t, root)); !slices.Equal(got, []string{"No matching tools found."}) {
		t.Errorf("results = %q", got)
	}
	if got := modelTools(t, opened, root); got != nil {
		t.Errorf("modelTools = %+v, want unset", got)
	}
}

// tool-search.ts:11 TOOL_SEARCH_TOOL_NAME, :12 DEFAULT_TOOL_SEARCH_LIMIT, :25 ToolSearchToolDetails, :55 createToolSearchTool.
func TestToolSearchToolDeclarationMatchesPi(t *testing.T) {
	if ToolName != "tool_search" || DefaultLimit != 8 {
		t.Errorf("ToolName %q, DefaultLimit %d", ToolName, DefaultLimit)
	}
	tool := CreateTool()
	if tool.Name != ToolName || !strings.Contains(tool.Description, "Searches over deferred tool metadata with BM25") {
		t.Errorf("tool %q: %q", tool.Name, tool.Description)
	}
	if _, ok := tool.Parameters["properties"].(map[string]any)["limit"]; !ok {
		t.Errorf("parameters %v lack limit", tool.Parameters)
	}
	if !slices.Equal(tool.Callers, []durable.ToolCaller{durable.CallerModel}) {
		t.Errorf("callers %v", tool.Callers)
	}
	encoded, err := json.Marshal(Details{Loaded: []string{"a", "b"}})
	if err != nil || string(encoded) != `{"loaded":["a","b"]}` {
		t.Errorf("details %s, %v", encoded, err)
	}
	// The default limit applies when the model gives none: nine matches load eight.
	c := chatSetup()
	for _, name := range []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9"} {
		addTool(t, c.registry, tool2(name))
	}
	if err := c.registry.Install(CreateExtension()); err != nil {
		t.Fatal(err)
	}
	c.faux.SetResponses([]ai.FauxResponseStep{call("tool_search", map[string]any{"query": "searchable"}, "c1"), done()})
	opened, root := c.open(t)
	run(t, root, "go")
	if got := modelTools(t, opened, root); got == nil || len(got.Add) != DefaultLimit {
		t.Errorf("modelTools = %+v, want %d loaded", got, DefaultLimit)
	}
}

func tool2(name string) *durable.ToolRegistration { return tool(name, "searchable tool "+name, true) }
