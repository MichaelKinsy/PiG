// Ports Pi 1.2.0 packages/durable/test/codemode-describe.test.ts.

package codemode

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	cm "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

func describeTool(name, description string, edit ...func(*durable.ToolRegistration)) *durable.ToolRegistration {
	var parameters map[string]any
	if err := json.Unmarshal([]byte(`{"type":"object","properties":{"path":{"type":"string","description":"File path"}},"required":["path"]}`), &parameters); err != nil {
		panic(err)
	}
	registration := &durable.ToolRegistration{Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
		return durable.ToolExecutionResult{}, nil
	}}
	registration.Name, registration.Description, registration.Parameters = name, description, parameters
	for _, apply := range edit {
		apply(registration)
	}
	return registration
}

var github = Namespace{Name: "github", Description: "GitHub MCP server", Prefix: "mcp__github__"}

func mustSection(t *testing.T, tools []*durable.ToolRegistration, namespaces []Namespace, budget int) *string {
	t.Helper()
	section, err := Section(tools, namespaces, budget)
	if err != nil {
		t.Fatal(err)
	}
	return section
}

// Pi 1.2.0 packages/durable/test/codemode-describe.test.ts:24 "keeps the tool description the same for every conversation"
func TestDescribeKeepsTheToolDescriptionTheSameForEveryConversation(t *testing.T) {
	text := Description(false)
	for _, want := range []string{"`await tools.<name>({ ...args })`", "searchTools(query", "The `codemode` section of the system prompt lists the tools scripts can call"} {
		if !strings.Contains(text, want) {
			t.Errorf("description lacks %q", want)
		}
	}
	if strings.Contains(text, "`models`") || !strings.Contains(Description(true), "`models`") {
		t.Error("only the description with models mentions `models`")
	}
}

// Pi 1.2.0 packages/durable/test/codemode-describe.test.ts:33 "lists callable tools as declarations, grouped by namespace, with namespace descriptions"
func TestDescribeListsCallableToolsAsDeclarationsGroupedByNamespace(t *testing.T) {
	section := *mustSection(t, []*durable.ToolRegistration{describeTool("read", "Read a file."), describeTool("mcp__github__list_issues", "List issues.")}, []Namespace{github}, 3000)
	for _, want := range []string{"### `read`\nRead a file.", "read(args: {", "## github\nGitHub MCP server", "Promise<string>"} {
		if !strings.Contains(section, want) {
			t.Errorf("section lacks %q:\n%s", want, section)
		}
	}
	// Tools before namespaces; a namespace's tools after its heading.
	read, heading, listed := strings.Index(section, "### `read`"), strings.Index(section, "## github"), strings.Index(section, "### `mcp__github__list_issues`")
	if read < 0 || read >= heading || heading >= listed {
		t.Errorf("order read %d, heading %d, listed %d", read, heading, listed)
	}
}

// Pi 1.2.0 packages/durable/test/codemode-describe.test.ts:48 "lists what fits the budget, every namespace before any is complete, and says what is left out"
func TestDescribeListsWhatFitsTheBudgetEveryNamespaceBeforeAnyIsCompleteAndSaysWhatIsLeftOut(t *testing.T) {
	var many []*durable.ToolRegistration
	for i := range 30 {
		many = append(many, describeTool(fmt.Sprintf("mcp__github__tool%d", i), strings.Repeat("x", 200)))
	}
	section := *mustSection(t, append([]*durable.ToolRegistration{describeTool("read", "Read a file.")}, many...), []Namespace{github}, 400)
	if !strings.Contains(section, "### `read`") || !strings.Contains(section, "## github (some tools not listed)") || strings.Contains(section, "tool29") {
		t.Errorf("section:\n%s", section)
	}
	if none := *mustSection(t, many, []Namespace{github}, 10); !strings.Contains(none, "## github (tools not listed)") {
		t.Errorf("over a tiny budget:\n%s", none)
	}
	if got := mustSection(t, nil, []Namespace{github}, 3000); got != nil {
		t.Errorf("without tools: %q, want none", *got)
	}
}

// Pi 1.2.0 packages/durable/test/codemode-describe.test.ts:58 "uses the longest matching namespace prefix"
func TestDescribeUsesTheLongestMatchingNamespacePrefix(t *testing.T) {
	section := *mustSection(t, []*durable.ToolRegistration{describeTool("mcp__github__x", "x")}, []Namespace{{Name: "git", Prefix: "mcp__git"}, github}, 3000)
	if !strings.Contains(section, "## github") || strings.Contains(section, "## git\n") {
		t.Errorf("section:\n%s", section)
	}
}

// selectCatalog: the cheapest remaining tool of each group per round, and a group whose next tool does not fit drops out
// while the others continue.
func TestSelectCatalogPlacesTheCheapestToolOfEachGroupPerRound(t *testing.T) {
	entry := func(name string, cost int) catalogEntry { return catalogEntry{name: name, cost: cost} }
	got := selectCatalog([]*catalogGroup{
		{entries: []catalogEntry{entry("a50", 50), entry("a5", 5)}},
		{entries: []catalogEntry{entry("b10", 10)}},
	}, 20)
	if len(got) != 2 || !got["a5"] || !got["b10"] {
		t.Errorf("shown %v, want a5 and b10", got)
	}
	got = selectCatalog([]*catalogGroup{
		{entries: []catalogEntry{entry("big", 30)}},
		{entries: []catalogEntry{entry("small", 5)}},
	}, 20)
	if len(got) != 1 || !got["small"] {
		t.Errorf("shown %v, want only small: a group that does not fit drops out and the others continue", got)
	}
}

// Pi 1.2.0 packages/durable/test/codemode-describe.test.ts:68 "renders the section from the conversation's callable tools into the system prompt"
func TestDescribeRendersTheSectionFromTheConversationsCallableToolsIntoTheSystemPrompt(t *testing.T) {
	setup := chatSetup()
	sandbox, err := cm.NewSandbox(cm.SandboxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	if err := setup.registry.Install(CreateExtension(ToolOptions{Sandbox: sandbox, Namespaces: []Namespace{github}})); err != nil {
		t.Fatal(err)
	}
	if err := setup.registry.Install(&durable.Extension{Name: "tools", Tools: []*durable.ToolRegistration{
		harness.DefineTool(*describeTool("read", "Read a file.")),
		harness.DefineTool(*describeTool("mcp__github__list_issues", "List issues.", func(tool *durable.ToolRegistration) { tool.Callers = []durable.ToolCaller{durable.CallerTools} })),
		harness.DefineTool(*describeTool("mcp__github__get_issue", "Get an issue.", func(tool *durable.ToolRegistration) { tool.Deferred = true })),
	}}); err != nil {
		t.Fatal(err)
	}
	setup.faux.SetResponses([]ai.FauxResponseStep{done()})
	opened, root := setup.open(t, storage.NewMemoryStorage())
	mustSubmitDone(t, root)
	var system ai.SystemMessage
	for _, entry := range allEntries(t, root) {
		if entry.Kind == "pi.system" {
			system = entry.Model[0].(ai.SystemMessage)
			break
		}
	}
	var section string
	for _, candidate := range system.Sections {
		if candidate.Name == "codemode" && candidate.Value != nil {
			section = *candidate.Value
		}
	}
	for _, want := range []string{"### `read`", "### `mcp__github__list_issues`"} {
		if !strings.Contains(section, want) {
			t.Errorf("section lacks %q:\n%s", want, section)
		}
	}
	// Deferred tools are found with searchTools(), not listed.
	// The code mode tool itself is not callable from scripts, nor are its internal tools.
	for _, unwanted := range []string{"mcp__github__get_issue", "### `codemode`", InternalPrefix} {
		if strings.Contains(section, unwanted) {
			t.Errorf("section has %q", unwanted)
		}
	}
	// The tool declaration carries the script grammar; pi-ai applies it only for models that support it.
	var declared *ai.ToolSchema
	for i := range system.ToolsAdded {
		if system.ToolsAdded[i].Name == "codemode" {
			declared = &system.ToolsAdded[i]
		}
	}
	if declared == nil || declared.ConstrainedSampling == nil || declared.ConstrainedSampling.Type != ai.ConstrainedSamplingGrammar || declared.ConstrainedSampling.Variants["openai_lark"] != cm.CodemodeSourceGrammar {
		t.Errorf("codemode declaration %+v", declared)
	}
	mustClose(t, opened)
}

// describe.ts declarationOf hands renderToolSample tool.parameters as authored, and the declaration's @default tag is
// JSON.stringify of the default: Node renders a decoded schema whose default is {"zeta":1,"alpha":2} as
// `// @default {"zeta":1,"alpha":2}` (pi-codemode 1.2.0 renderToolSample), not with sorted members.
func TestDescribeRendersADefaultWithItsAuthoredMemberOrder(t *testing.T) {
	tool := describeTool("find", "Find.")
	if err := json.Unmarshal([]byte(`{"name":"find","description":"Find.","parameters":{"type":"object","properties":{"where":{"type":"object","default":{"zeta":1,"alpha":2}}}}}`), &tool.ToolSchema); err != nil {
		t.Fatal(err)
	}
	sample, err := sampleOf(tool)
	if err != nil {
		t.Fatal(err)
	}
	if want := `// @default {"zeta":1,"alpha":2}`; !strings.Contains(sample, want) {
		t.Errorf("sample lacks %s:\n%s", want, sample)
	}
}
