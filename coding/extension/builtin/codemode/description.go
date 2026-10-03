// Package codemode is the built-in `codemode` extension: the tool that lets the model write JavaScript that calls
// other tools. The script runs in the sandbox of package codemode (docs/specs/builtin-codemode-tool-search.md).
//
// Scripts use `tools`, `ALL_TOOLS`, `text()`, `image()`, `exit()`, `store()`/`load()`, `console.*`, and
// `return <value>`, may start with a `// @options:` line, and reach the model catalog, classifiers, and image models
// through `models.*`. Results start with a "Script completed" or "Script failed" header.
//
// Scripts can call the agent loop's nested tools: active `direct` tools and every `codemode` or `deferred` tool. Nested
// calls run through the agent loop's tool pipeline (`ToolContext.ExecuteTool`), so validation, `tool_call` and
// `tool_result` hooks, and permission checks apply exactly as for direct calls. Only the script's output reaches the
// model; nested results do not.
//
// Ports packages/coding-agent/src/extensions/codemode/{tool,execute,renderer,index}.ts.
package codemode

import (
	"encoding/json"
	"math"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	sandbox "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pigdocs"
)

// ToolName is the name of the tool.
const ToolName = "codemode"

// DefaultInlineBudget is the default token budget for tool declarations in the description, in estimated tokens.
const DefaultInlineBudget = 3000

// charsPerToken is the number of characters per token when estimating the cost of a tool section.
const charsPerToken = 4

// DocsPath is the reference for scripts: globals, tool results, `store()`, the `models` API, and limits. It is the
// codemode page of the documentation bundle PiG materializes under its config root, the directory the system prompt's
// docs section names.
//
// Ports packages/coding-agent/src/extensions/codemode/tool.ts (CODEMODE_DOCS_PATH).
func DocsPath() string {
	// pig additive (D22): Pi names the docs of its installed package; PiG names its materialized documentation bundle.
	return filepath.Join(codingagent.ConfigRoot(), pigdocs.SubDir, "codemode.md")
}

// textOutputSchema is what a script sees of a tool without an output schema: its text output.
var textOutputSchema = json.RawMessage(`{"type":"string"}`)

// DescriptionOptions configures CreateDescription.
type DescriptionOptions struct {
	// Models names the `models` global; only for tools created with model access.
	Models bool
	// Namespaces holds the namespace of each tool, by tool name. Tools of one namespace are listed under one heading.
	Namespaces map[string]extension.ToolNamespace
	// Deferred names the tools that are callable but never listed. They do not affect the description at all, so it
	// stays the same while MCP servers connect or change their tools.
	Deferred map[string]bool
	// InlineBudget is the estimated tokens (characters / 4) the tool sections may use; tools that do not fit are
	// left out like deferred tools. Nil lists every tool that is not deferred.
	InlineBudget *int
}

// toDeclaration is what a script sees of a tool. Tools without an output schema resolve to their text output.
func toDeclaration(tool extension.AgentTool) sandbox.Tool {
	output := tool.OutputSchema
	if len(output) == 0 {
		output = textOutputSchema
	}
	return sandbox.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.Parameters, OutputSchema: output}
}

// callableTools are the tools a script may call: every given tool except the codemode tool itself.
func callableTools(tools []extension.AgentTool) []extension.AgentTool {
	return slices.DeleteFunc(slices.Clone(tools), func(tool extension.AgentTool) bool { return tool.Name == ToolName })
}

// renderToolSection is `### \`id\` (\`raw name\`)` followed by the tool's description and declaration.
func renderToolSection(declaration sandbox.Tool) string {
	id := sandbox.ToCodemodeIdentifier(declaration.Name)
	heading := "### `" + id + "` (`" + declaration.Name + "`)"
	if id == declaration.Name {
		heading = "### `" + id + "`"
	}
	return heading + "\n" + jsstring.Trim(sandbox.RenderToolSample(declaration, 0))
}

type catalogEntry struct {
	name    string
	section string
	cost    int
}

type catalogGroup struct {
	namespace *extension.ToolNamespace
	entries   []catalogEntry
}

// selectCatalog picks the tool sections that fit the budget: in each round every group (tools without a namespace
// first, then namespaces by name) places its cheapest remaining tool; a group whose next tool does not fit drops out
// while the others continue. Every namespace is represented before any namespace is complete.
func selectCatalog(groups []*catalogGroup, budget *int) map[string]bool {
	shown := map[string]bool{}
	queues := make([][]catalogEntry, 0, len(groups))
	for _, group := range groups {
		if budget == nil {
			for _, entry := range group.entries {
				shown[entry.name] = true
			}
			continue
		}
		queue := slices.Clone(group.entries)
		slices.SortStableFunc(queue, func(a, b catalogEntry) int { return a.cost - b.cost })
		queues = append(queues, queue)
	}
	if budget == nil {
		return shown
	}
	remaining := *budget
	active := slices.DeleteFunc(queues, func(q []catalogEntry) bool { return len(q) == 0 })
	for len(active) > 0 {
		next := active[:0]
		for _, queue := range active {
			head := queue[0]
			if head.cost > remaining {
				continue
			}
			remaining -= head.cost
			shown[head.name] = true
			queue = queue[1:]
			if len(queue) > 0 {
				next = append(next, queue)
			}
		}
		active = next
	}
	return shown
}

var localeCollator = collate.New(language.Und)

// CreateDescription is the model-facing description: the intro, one line per script global (with `models` when
// options.Models is set), the shared MCP types when listed tools need them, and one section per listed tool, grouped by
// namespace. Deferred tools are never listed and do not affect the description at all, so it stays the same while MCP
// servers connect or change their tools. Tool sections are limited to the inline budget.
func CreateDescription(tools []extension.AgentTool, options DescriptionOptions) string {
	var declarations []sandbox.Tool
	for _, tool := range callableTools(tools) {
		if !options.Deferred[tool.Name] {
			declarations = append(declarations, toDeclaration(tool))
		}
	}
	groups := map[string]*catalogGroup{"": {}}
	order := []string{""}
	for _, declaration := range declarations {
		var namespace *extension.ToolNamespace
		if ns, ok := options.Namespaces[declaration.Name]; ok {
			namespace = &ns
		}
		key := ""
		if namespace != nil {
			key = "ns:" + namespace.Name
		}
		group, ok := groups[key]
		if !ok {
			group = &catalogGroup{namespace: namespace}
			groups[key] = group
			order = append(order, key)
		}
		section := renderToolSection(declaration)
		group.entries = append(group.entries, catalogEntry{
			name: declaration.Name, section: section,
			cost: int(math.Ceil(float64(jsstring.Length(section)) / charsPerToken)),
		})
	}
	ordered := make([]*catalogGroup, len(order))
	for i, key := range order {
		ordered[i] = groups[key]
	}
	slices.SortStableFunc(ordered, func(a, b *catalogGroup) int {
		switch {
		case a.namespace == nil && b.namespace == nil:
			return 0
		case a.namespace == nil:
			return -1
		case b.namespace == nil:
			return 1
		}
		return localeCollator.CompareString(a.namespace.Name, b.namespace.Name)
	})
	shown := selectCatalog(ordered, options.InlineBudget)

	sections := []string{descriptionIntro, describeGlobals(options.Models)}
	for _, declaration := range declarations {
		if shown[declaration.Name] && sandbox.McpStructuredContentSchema(declaration.OutputSchema) != nil {
			sections = append(sections, "Shared MCP Types:\n```ts\n"+sandbox.McpTypescriptPreamble+"\n```")
			break
		}
	}
	if len(declarations) == 0 {
		return strings.Join(sections, "\n\n")
	}

	toolSections := []string{"Nested tools:"}
	for _, group := range ordered {
		var visible []catalogEntry
		for _, entry := range group.entries {
			if shown[entry.name] {
				visible = append(visible, entry)
			}
		}
		if namespace := group.namespace; namespace != nil {
			// Only tools that did not fit the budget are counted as not listed here.
			listing := ""
			switch {
			case len(visible) == len(group.entries):
			case len(visible) == 0:
				listing = " (tools not listed)"
			default:
				listing = " (some tools not listed)"
			}
			heading := "## " + namespace.Name + listing
			if description := jsstring.Trim(namespace.Description); description != "" {
				heading += "\n" + description
			}
			toolSections = append(toolSections, heading)
		}
		for _, entry := range visible {
			toolSections = append(toolSections, entry.section)
		}
	}
	sections = append(sections, strings.Join(toolSections, "\n\n"))
	return strings.Join(sections, "\n\n")
}
