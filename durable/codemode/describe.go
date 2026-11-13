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
	"github.com/MichaelKinsy/PiG/internal/jsstring/localecompare"
)

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
	return cm.RenderToolSample(declaration, cm.ToolRenderOptions{}), nil
}

// NamespacesOf is the namespace of each tool the extensions bring, by tool name: its extension's `Namespace`. A tool several
// extensions name belongs to the first one's (with none when the first declares none).
func NamespacesOf(extensions []*durable.Extension) map[string]*durable.ToolNamespace {
	namespaces := map[string]*durable.ToolNamespace{}
	seen := map[string]bool{}
	for _, extension := range extensions {
		for _, tool := range extension.Tools {
			if seen[tool.Name] {
				continue
			}
			seen[tool.Name] = true
			if extension.Namespace != nil {
				namespaces[tool.Name] = extension.Namespace
			}
		}
	}
	return namespaces
}

// declaredNamespaces are the namespaces the extensions declare, by name (the first declaration of a name wins), sorted by name.
func declaredNamespaces(extensions []*durable.Extension) []*durable.ToolNamespace {
	byName := map[string]bool{}
	var declared []*durable.ToolNamespace
	for _, extension := range extensions {
		if namespace := extension.Namespace; namespace != nil && !byName[namespace.Name] {
			byName[namespace.Name] = true
			declared = append(declared, namespace)
		}
	}
	slices.SortStableFunc(declared, func(a, b *durable.ToolNamespace) int { return localecompare.Compare(a.Name, b.Name) })
	return declared
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
	namespace *durable.ToolNamespace
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

// Section is the `codemode` section: the tools scripts can call (ungrouped ones first), then every namespace the selected
// extensions declare, by name, as `## <name>` and its description, each followed by its tools' declarations as far as the
// budget allows. Headers carry no counts, so the text changes only with the declared tools. Nil without callable tools and
// namespaces.
func Section(tools []*durable.ToolRegistration, extensions []*durable.Extension, budget int) (*string, error) {
	namespaces := declaredNamespaces(extensions)
	if len(tools) == 0 && len(namespaces) == 0 {
		return nil, nil
	}
	toolNamespaces := NamespacesOf(extensions)
	ungrouped := &catalogGroup{}
	ordered := []*catalogGroup{ungrouped}
	groups := map[string]*catalogGroup{}
	for _, namespace := range namespaces {
		group := &catalogGroup{namespace: namespace}
		groups[namespace.Name] = group
		ordered = append(ordered, group)
	}
	for _, tool := range tools {
		group := ungrouped
		if namespace := toolNamespaces[tool.Name]; namespace != nil {
			group = groups[namespace.Name]
		}
		section, err := renderToolSection(tool)
		if err != nil {
			return nil, err
		}
		//portlint:allow numbers Math.ceil of a string length over a positive constant is a small non-negative integer (describe.ts:131)
		group.entries = append(group.entries, catalogEntry{name: tool.Name, section: section, cost: int(math.Ceil(float64(jsstring.Length(section)) / charsPerToken))})
	}
	shown := selectCatalog(ordered, budget)

	toolSections := []string{"Tools scripts can call (`await tools.<name>(args)`):"}
	if len(shown) < len(tools) {
		toolSections = append(toolSections, "Some tools are not listed: find them with `searchTools()`.")
	}
	for _, group := range ordered {
		if group.namespace != nil {
			toolSections = append(toolSections, "## "+group.namespace.Name+"\n"+jsstring.Trim(group.namespace.Description))
		}
		for _, entry := range group.entries {
			if shown[entry.name] {
				toolSections = append(toolSections, entry.section)
			}
		}
	}
	text := strings.Join(toolSections, "\n\n")
	return &text, nil
}
