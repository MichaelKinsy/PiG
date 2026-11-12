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

var github = durable.ToolNamespace{Name: "mcp__github", Description: "GitHub MCP server", Instructions: "Pass repositories as owner/name."}

// describeExtension is Pi's `extension(name, tools, namespace)` of codemode-describe.test.ts: an extension bringing tools, in
// namespace when given.
func describeExtension(name string, tools []*durable.ToolRegistration, namespace ...durable.ToolNamespace) *durable.Extension {
	extension := &durable.Extension{Name: name, Tools: tools}
	if len(namespace) > 0 {
		extension.Namespace = &namespace[0]
	}
	return extension
}

func mustSection(t *testing.T, tools []*durable.ToolRegistration, extensions []*durable.Extension, budget int) *string {
	t.Helper()
	section, err := Section(tools, extensions, budget)
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

// Ports packages/durable/test/codemode-describe.test.ts (Pi 5e24c6b1f) "puts a tool in its extension's namespace; the first
// extension naming a tool wins"
func TestDescribePutsAToolInItsExtensionsNamespaceTheFirstExtensionNamingAToolWins(t *testing.T) {
	issues, shared := describeTool("list_issues", "List issues."), describeTool("read", "Read a file.")
	namespaces := NamespacesOf([]*durable.Extension{describeExtension("coding", []*durable.ToolRegistration{shared}), describeExtension("github", []*durable.ToolRegistration{issues, shared}, github)})
	if got := namespaces["list_issues"]; got == nil || *got != github {
		t.Errorf("list_issues is in %+v, want %+v", got, github)
	}
	// `read` came first from an extension without a namespace: ungrouped.
	if _, has := namespaces["read"]; has {
		t.Errorf("read is in %+v, want ungrouped", namespaces["read"])
	}
	reversed := NamespacesOf([]*durable.Extension{describeExtension("github", []*durable.ToolRegistration{issues, shared}, github), describeExtension("coding", []*durable.ToolRegistration{shared})})
	if got := reversed["read"]; got == nil || *got != github {
		t.Errorf("read in the reversed order is in %+v, want %+v", got, github)
	}
}

// Ports codemode-describe.test.ts "lists ungrouped tools first, then each namespace's heading and description, then its
// declarations"
func TestDescribeListsUngroupedToolsFirstThenEachNamespacesHeadingAndDescriptionThenItsDeclarations(t *testing.T) {
	read, issues := describeTool("read", "Read a file."), describeTool("list_issues", "List issues.")
	section := *mustSection(t, []*durable.ToolRegistration{read, issues}, []*durable.Extension{describeExtension("coding", []*durable.ToolRegistration{read}), describeExtension("github", []*durable.ToolRegistration{issues}, github)}, 3000)
	for _, want := range []string{"### `read`\nRead a file.", "read(args: {", "## mcp__github\nGitHub MCP server", "Promise<string>"} {
		if !strings.Contains(section, want) {
			t.Errorf("section lacks %q:\n%s", want, section)
		}
	}
	// Instructions only through describeNamespace().
	if strings.Contains(section, "owner/name") {
		t.Errorf("section has the instructions:\n%s", section)
	}
	readAt, heading, listed := strings.Index(section, "### `read`"), strings.Index(section, "## mcp__github"), strings.Index(section, "### `list_issues`")
	if readAt < 0 || readAt >= heading || heading >= listed {
		t.Errorf("order read %d, heading %d, listed %d", readAt, heading, listed)
	}
}

// Ports codemode-describe.test.ts "lists a namespace without tools, and nothing without tools and namespaces"
func TestDescribeListsANamespaceWithoutToolsAndNothingWithoutToolsAndNamespaces(t *testing.T) {
	section := *mustSection(t, nil, []*durable.Extension{describeExtension("github", nil, github)}, 3000)
	if !strings.Contains(section, "## mcp__github\nGitHub MCP server") {
		t.Errorf("section:\n%s", section)
	}
	if got := mustSection(t, nil, []*durable.Extension{describeExtension("coding", nil)}, 3000); got != nil {
		t.Errorf("without tools and namespaces: %q, want none", *got)
	}
}

// Ports codemode-describe.test.ts "keeps the section identical before and after a namespace's (deferred) tools appear"
func TestDescribeKeepsTheSectionIdenticalBeforeAndAfterANamespacesDeferredToolsAppear(t *testing.T) {
	read := describeTool("read", "Read a file.")
	before := mustSection(t, []*durable.ToolRegistration{read}, []*durable.Extension{describeExtension("coding", []*durable.ToolRegistration{read}), describeExtension("github", nil, github)}, 3000)
	// The server connected: its tools are installed, deferred, and callable by scripts.
	issues := describeTool("list_issues", "List issues.", func(tool *durable.ToolRegistration) { tool.Deferred = true })
	extensions := []*durable.Extension{describeExtension("coding", []*durable.ToolRegistration{read}), describeExtension("github", []*durable.ToolRegistration{issues}, github)}
	// The section's caller passes the non-deferred callable tools (deferred ones are found with searchTools()).
	after := mustSection(t, []*durable.ToolRegistration{read}, extensions, 3000)
	if before == nil || after == nil || *before != *after {
		t.Errorf("before:\n%v\nafter:\n%v", before, after)
	}
}

// Ports codemode-describe.test.ts "lists what fits the budget, every namespace before any is complete, with no counts in headings"
func TestDescribeListsWhatFitsTheBudgetEveryNamespaceBeforeAnyIsCompleteWithNoCountsInHeadings(t *testing.T) {
	var many []*durable.ToolRegistration
	for i := range 30 {
		many = append(many, describeTool(fmt.Sprintf("tool%d", i), strings.Repeat("x", 200)))
	}
	read := describeTool("read", "Read a file.")
	extensions := []*durable.Extension{describeExtension("coding", []*durable.ToolRegistration{read}), describeExtension("github", many, github)}
	section := *mustSection(t, append([]*durable.ToolRegistration{read}, many...), extensions, 400)
	for _, want := range []string{"### `read`", "Some tools are not listed: find them with `searchTools()`.", "## mcp__github\nGitHub MCP server"} {
		if !strings.Contains(section, want) {
			t.Errorf("section lacks %q:\n%s", want, section)
		}
	}
	if strings.Contains(section, "tool29") || strings.Contains(section, "not listed)") {
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

// Pi 1.2.0 packages/durable/test/codemode-describe.test.ts:68 "renders the section from the conversation's extensions and callable tools into the system prompt"
func TestDescribeRendersTheSectionFromTheConversationsExtensionsAndCallableToolsIntoTheSystemPrompt(t *testing.T) {
	setup := chatSetup()
	sandbox, err := cm.NewSandbox(cm.SandboxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	if err := setup.registry.Install(CreateExtension(ToolOptions{Sandbox: sandbox})); err != nil {
		t.Fatal(err)
	}
	if err := setup.registry.Install(&durable.Extension{Name: "tools", Tools: []*durable.ToolRegistration{harness.DefineTool(*describeTool("read", "Read a file."))}}); err != nil {
		t.Fatal(err)
	}
	if err := setup.registry.Install(&durable.Extension{Name: "mcp:github", Namespace: &github, Tools: []*durable.ToolRegistration{
		harness.DefineTool(*describeTool("list_issues", "List issues.", func(tool *durable.ToolRegistration) { tool.Callers = []durable.ToolCaller{durable.CallerTools} })),
		harness.DefineTool(*describeTool("get_issue", "Get an issue.", func(tool *durable.ToolRegistration) { tool.Deferred = true })),
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
	for _, want := range []string{"### `read`", "## mcp__github\nGitHub MCP server", "### `list_issues`"} {
		if !strings.Contains(section, want) {
			t.Errorf("section lacks %q:\n%s", want, section)
		}
	}
	// Deferred tools are found with searchTools(), not listed.
	// The code mode tool itself is not callable from scripts, nor are its internal tools.
	for _, unwanted := range []string{"get_issue", "### `codemode`", InternalPrefix} {
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
