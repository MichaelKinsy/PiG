// Ports packages/durable/src/codemode/describe.ts.

package codemode

import (
	"encoding/json"
	"math"
	"slices"
	"strings"

	cm "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Namespace is a group of tools named by a common prefix, such as an MCP server's `mcp__github__`.
type Namespace struct {
	Name         string
	Description  string
	Instructions string
	// Prefix: tool names starting with it belong to the namespace; the longest matching prefix wins.
	Prefix string
}

// DefaultInlineBudget is the default token budget of the tool declarations in the `codemode` section, as in the coding
// agent.
const DefaultInlineBudget = 3000

// charsPerToken estimates the cost of a tool section.
const charsPerToken = 4

const textOutputSchema = `{"type":"string"}`

const descriptionIntro = "Run JavaScript that calls other tools. The input is raw JavaScript (not JSON, no code fence), run as an async function body in a QuickJS sandbox: top-level `await` and `return` work. No Node, file system, network, or timers.\n" +
	"- `await tools.<name>({ ...args })` resolves to a string, or an object if the tool's declaration says so, and rejects with an Error on failure. A tool whose declaration returns an object resolves on failure too when it returned that object; check its fields. Calls still running when the script ends are cancelled.\n" +
	"- The script survives a restart: it resumes where it was, without running finished calls again.\n" +
	"- Optional first line: `// @options: {\"max_output_tokens\": 10000, \"timeout_ms\": 60000}`"

// Description is the code mode tool's description: the script environment and its globals, the same for every
// conversation.
func Description(models bool) string {
	globals := strings.Join([]string{
		"Globals:",
		"- `text(value)`, `image(dataUrlOrImageBlock)`, `console.log(...)`, and top-level `return` add output; `exit()` ends the script. With several text items, each starts with a `==> text N/M <==` line, and `console` lines follow the other output in one `<console_output>` block. `image()` also saves the image to a temp file and the result names its path.",
		"- `store(key, value)` and `load(key)` keep JSON values across codemode calls.",
		"- `ALL_TOOLS`, `await searchTools(query, { limit?, namespace? })`, `await describeTool(name)`, `await describeNamespace(name)`: find tools the `codemode` section does not list.",
	}, "\n")
	parts := []string{descriptionIntro, globals}
	if models {
		parts = append(parts, ModelsDescription)
	}
	parts = append(parts, "The `codemode` section of the system prompt lists the tools scripts can call, by namespace, as far as space allows.")
	return strings.Join(parts, "\n\n")
}

// declarationOf is what a script sees of a tool: its declaration. Tools without an output schema resolve to their text
// output.
func declarationOf(tool *durable.ToolRegistration) (cm.Tool, error) {
	input, err := tool.ParametersJSON()
	if err != nil {
		return cm.Tool{}, err
	}
	output := json.RawMessage(textOutputSchema)
	if tool.StructuredOutputSchema != nil {
		//portlint:allow mapkeyorder StructuredOutputSchema is a Go map with no recorded member order; renderToolSample sorts property names, and sorted members are how a Go-authored schema stringifies (ai.ToolSchema.ParametersJSON)
		if output, err = json.Marshal(tool.StructuredOutputSchema); err != nil {
			return cm.Tool{}, err
		}
	}
	return cm.Tool{Name: tool.Name, Description: tool.Description, InputSchema: input, OutputSchema: output}, nil
}

// sampleOf is `toolSample` of a tool: its description and TypeScript declaration.
func sampleOf(tool *durable.ToolRegistration) (string, error) {
	declaration, err := declarationOf(tool)
	if err != nil {
		return "", err
	}
	return cm.RenderToolSample(declaration, cm.ToolRenderOptions{})
}

// namespaceOf is the namespace of a tool: the one with the longest prefix its name starts with.
func namespaceOf(name string, namespaces []Namespace) *Namespace {
	var found *Namespace
	for i := range namespaces {
		namespace := &namespaces[i]
		if strings.HasPrefix(name, namespace.Prefix) && (found == nil || jsstring.Length(namespace.Prefix) > jsstring.Length(found.Prefix)) {
			found = namespace
		}
	}
	return found
}

// renderToolSection is `### `id` (`raw name`)` followed by the tool's description and declaration.
func renderToolSection(tool *durable.ToolRegistration) (string, error) {
	id := cm.ToCodemodeIdentifier(tool.Name)
	heading := "### `" + id + "` (`" + tool.Name + "`)"
	if id == tool.Name {
		heading = "### `" + id + "`"
	}
	sample, err := sampleOf(tool)
	if err != nil {
		return "", err
	}
	return heading + "\n" + jsstring.Trim(sample), nil
}

type catalogEntry struct {
	name    string
	section string
	cost    int
}

type catalogGroup struct {
	namespace *Namespace
	entries   []catalogEntry
}

// selectCatalog picks the tool sections that fit the budget: in each round every group (tools without a namespace first,
// then namespaces by name) places its cheapest remaining tool; a group whose next tool does not fit drops out while the
// others continue. Every namespace is represented before any namespace is complete.
func selectCatalog(groups []*catalogGroup, budget int) map[string]bool {
	queues := make([][]catalogEntry, len(groups))
	for i, group := range groups {
		queues[i] = slices.Clone(group.entries)
		slices.SortStableFunc(queues[i], func(a, b catalogEntry) int { return a.cost - b.cost })
	}
	shown := map[string]bool{}
	remaining := budget
	var active [][]catalogEntry
	for _, queue := range queues {
		if len(queue) > 0 {
			active = append(active, queue)
		}
	}
	for len(active) > 0 {
		var next [][]catalogEntry
		for _, queue := range active {
			entry := queue[0]
			if entry.cost > remaining {
				continue
			}
			remaining -= entry.cost
			shown[entry.name] = true
			if queue = queue[1:]; len(queue) > 0 {
				next = append(next, queue)
			}
		}
		active = next
	}
	return shown
}

// Section is the `codemode` section: the tools scripts can call, grouped by namespace, with their declarations as far as
// the budget allows. Nil without callable tools.
func Section(tools []*durable.ToolRegistration, namespaces []Namespace, budget int) (*string, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	groups := map[string]*catalogGroup{"": {}}
	order := []string{""}
	for _, tool := range tools {
		namespace := namespaceOf(tool.Name, namespaces)
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
		section, err := renderToolSection(tool)
		if err != nil {
			return nil, err
		}
		//portlint:allow numbers Math.ceil of a string length over a positive constant is a small non-negative integer (describe.ts:131)
		group.entries = append(group.entries, catalogEntry{name: tool.Name, section: section, cost: int(math.Ceil(float64(jsstring.Length(section)) / charsPerToken))})
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
		return jsstring.LocaleCompare(a.namespace.Name, b.namespace.Name)
	})
	shown := selectCatalog(ordered, budget)

	toolSections := []string{"Tools scripts can call (`await tools.<name>(args)`):"}
	for _, group := range ordered {
		if len(group.entries) == 0 {
			continue
		}
		var visible []catalogEntry
		for _, entry := range group.entries {
			if shown[entry.name] {
				visible = append(visible, entry)
			}
		}
		if group.namespace != nil {
			listing := ""
			switch {
			case len(visible) == len(group.entries):
			case len(visible) == 0:
				listing = " (tools not listed)"
			default:
				listing = " (some tools not listed)"
			}
			heading := "## " + group.namespace.Name + listing
			if description := jsstring.Trim(group.namespace.Description); description != "" {
				heading += "\n" + description
			}
			toolSections = append(toolSections, heading)
		} else if len(visible) < len(group.entries) {
			toolSections = append(toolSections, "Some tools are not listed: find them with `searchTools()`.")
		}
		for _, entry := range visible {
			toolSections = append(toolSections, entry.section)
		}
	}
	text := strings.Join(toolSections, "\n\n")
	return &text, nil
}
