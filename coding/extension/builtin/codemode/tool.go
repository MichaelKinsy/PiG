package codemode

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"

	sandbox "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// Options configures the extension.
type Options struct {
	// GetSettings returns the effective settings, read on every use: `codemode.mode` and `codemode.inlineBudget`.
	// Nil means the defaults (mode `on`, the default budget).
	GetSettings func() extension.Settings
	// Mode overrides the `codemode.mode` setting: [ModeOn] or [ModeOnly]. Empty reads the setting.
	//
	// Ports packages/coding-agent/src/extensions/codemode/index.ts (CodemodeExtensionOptions.mode).
	Mode string
	// InlineBudget overrides the `codemode.inlineBudget` setting. Nil reads the setting.
	//
	// Ports packages/coding-agent/src/extensions/codemode/index.ts (CodemodeExtensionOptions.inlineBudget).
	InlineBudget *int
	// CacheDir holds wazero's on-disk compilation cache for the sandbox module; empty compiles in memory.
	CacheDir string
	// Models declares and serves the `models` namespace of the tool [Definition] (createCodemodeToolDefinition's
	// `models`, false by default). The [Extension] sets it unless DisableModels is set.
	Models bool
	// DisableModels leaves `models` out of the tool [Extension] registers. Upstream's createCodemodeExtension serves it
	// unless its `models` option is false (index.ts `options.models ?? true`).
	DisableModels bool
}

// Mode is how the tool presents tools that are both declared and callable from scripts.
const (
	ModeOn   = "on"
	ModeOnly = "only"
)

func (o Options) mode() string {
	if o.Mode == ModeOn || o.Mode == ModeOnly {
		return o.Mode
	}
	if o.GetSettings != nil {
		if codemode, ok := o.GetSettings()["codemode"].(map[string]any); ok && codemode["mode"] == ModeOnly {
			return ModeOnly
		}
	}
	return ModeOn
}

func (o Options) inlineBudget() *int {
	if o.InlineBudget != nil {
		return o.InlineBudget
	}
	if o.GetSettings != nil {
		if codemode, ok := o.GetSettings()["codemode"].(map[string]any); ok {
			if budget, ok := codemode["inlineBudget"].(float64); ok && !math.IsInf(budget, 0) && !math.IsNaN(budget) && budget >= 0 {
				// A budget beyond the int range still holds every tool section.
				value := int(min(budget, math.MaxInt32))
				return &value
			}
		}
	}
	return nil
}

// promptSnippet and promptGuidelines are upstream's codemodeToolSystemPromptContribution.
const promptSnippet = "Run JavaScript that calls other tools"

var promptGuidelines = []string{"Use codemode to batch independent tool calls (Promise.allSettled), chain them, or filter large output, instead of many separate calls."}

// inputSchema is upstream's codemodeSchema.
var inputSchema = json.RawMessage(`{"type":"object","properties":{"code":{"description":"Raw JavaScript source.","type":"string"}},"required":["code"]}`)

// describeOutput is what a script call resolves to, in one line: `a string`, the field names of an object
// (`{ output, exit_code, full_output_path? }`), or the rendered type for anything else.
//
// Ports packages/coding-agent/src/extensions/codemode/tool.ts (describeOutput).
func describeOutput(schema json.RawMessage) (string, error) {
	rendered, err := sandbox.RenderToolOutputType(schema)
	if err != nil {
		return "", err
	}
	if rendered == "string" {
		return "a string", nil
	}
	if object := objectOf(schema); object != nil && sandbox.McpStructuredContentSchema(schema) == nil {
		// upstream: `typeof properties === "object"` also holds for an array, whose keys are its indexes.
		if typ, _ := object.Get("type"); string(typ) == `"object"` && (isRecord(field(schema, "properties")) || jsonKind(field(schema, "properties")) == '[') {
			required := map[string]bool{}
			var names []json.RawMessage
			if json.Unmarshal(field(schema, "required"), &names) == nil {
				for _, name := range names {
					if value, ok := stringArg(name); ok {
						required[value] = true
					}
				}
			}
			var keys []string
			if properties := objectOf(field(schema, "properties")); properties != nil {
				keys = properties.Keys()
			} else {
				var items []json.RawMessage
				_ = json.Unmarshal(field(schema, "properties"), &items)
				for i := range items {
					keys = append(keys, strconv.Itoa(i))
				}
			}
			fields := make([]string, 0, len(keys))
			for _, name := range keys {
				if required[name] {
					fields = append(fields, name)
				} else {
					fields = append(fields, name+"?")
				}
			}
			return "`{ " + strings.Join(fields, ", ") + " }`", nil
		}
	}
	return "`" + jsWhitespace.ReplaceAllString(rendered, " ") + "`", nil
}

// jsWhitespace is JavaScript's `\s+`.
var jsWhitespace = lazyregexp.New(`[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+`)

// describeScriptCall is a declared tool's description followed by how scripts call it and what the call resolves to.
// The arguments are the tool's declared parameters, so they are not repeated.
//
// Ports packages/coding-agent/src/extensions/codemode/tool.ts (describeScriptCall).
func describeScriptCall(tool extension.AgentTool) (string, error) {
	output, err := describeOutput(toDeclaration(tool, nil).OutputSchema)
	if err != nil {
		return "", err
	}
	return jsstring.Trim(tool.Description) + "\n\nCodemode: `tools." + sandbox.ToCodemodeIdentifier(tool.Name) + "(args)` resolves to " + output + ".", nil
}

// prepareLoadout is how the tool presents tools that are both declared and callable from scripts:
//   - `on`: their descriptions say how scripts call them, and the codemode description lists only the callable tools
//     without `direct` exposure.
//   - `only`: the codemode description lists every callable tool, and requests leave out the declarations of active
//     `direct` tools.
//
// Listed tools carry their prompt guidelines, which the system prompt only has for declared tools.
//
// Listing by exposure, not by the active set, keeps the codemode description unchanged when `tool_search` loads a
// tool, so loads do not redeclare codemode.
func prepareLoadout(loadout extension.ToolLoadout, options Options) *extension.ToolLoadoutChanges {
	mode := options.mode()
	isDirect := func(tool extension.AgentTool) bool {
		return loadout.GetExposure(tool.Name) == extension.ToolExposureDirect
	}
	callable := callableTools(loadout.Callable)
	callableNames := map[string]bool{}
	for _, tool := range callable {
		callableNames[tool.Name] = true
	}
	descriptions := map[string]string{}
	if mode == ModeOn {
		for _, tool := range loadout.Declared {
			if callableNames[tool.Name] {
				description, err := describeScriptCall(tool)
				if err != nil {
					// upstream: prepareLoadout throws; the session reports it as a prepare_loadout extension error (extension.ToolPrepareLoadoutFunc).
					panic(err)
				}
				descriptions[tool.Name] = description
			}
		}
	}
	listed := callable
	if mode != ModeOnly {
		listed = slices.DeleteFunc(slices.Clone(callable), isDirect)
	}
	namespaces := map[string]extension.ToolNamespace{}
	deferred := map[string]bool{}
	for _, tool := range listed {
		if namespace := loadout.GetNamespace(tool.Name); namespace != nil {
			namespaces[tool.Name] = *namespace
		}
		if loadout.GetExposure(tool.Name) == extension.ToolExposureDeferred {
			deferred[tool.Name] = true
		}
	}
	// Listed tools carry their prompt guidelines, which the system prompt only has for declared tools.
	guidelines := map[string][]string{}
	for _, tool := range listed {
		guidelines[tool.Name] = loadout.GetPromptGuidelines(tool.Name)
	}
	budget := options.inlineBudget()
	if budget == nil {
		defaultBudget := DefaultInlineBudget
		budget = &defaultBudget
	}
	description, err := CreateDescription(listed, DescriptionOptions{Models: options.Models, Namespaces: namespaces, Guidelines: guidelines, Deferred: deferred, InlineBudget: budget})
	if err != nil {
		panic(err) // upstream: see above, a thrown prepareLoadout.
	}
	descriptions[ToolName] = description
	hidden := []string{}
	if mode == ModeOnly {
		declared := map[string]bool{}
		for _, tool := range loadout.Declared {
			declared[tool.Name] = true
		}
		for _, tool := range callable {
			if isDirect(tool) && declared[tool.Name] {
				hidden = append(hidden, tool.Name)
			}
		}
	}
	return &extension.ToolLoadoutChanges{Descriptions: descriptions, HiddenDeclarations: hidden}
}

// Definition is the tool definition of `codemode`, without the registration flag: Extension registers it inactive.
//
// Ports packages/coding-agent/src/extensions/codemode/tool.ts (createCodemodeToolDefinition).
func Definition(options Options) extension.ToolDefinition {
	// No tools are listed, so no schema reference is decoded and rendering cannot fail.
	description, _ := CreateDescription(nil, DescriptionOptions{Models: options.Models})
	return extension.ToolDefinition{
		Name:             ToolName,
		Label:            ToolName,
		Description:      description,
		PromptSnippet:    promptSnippet,
		PromptGuidelines: promptGuidelines,
		Parameters:       inputSchema,
		// Scripts must not start other scripts.
		Exposure: extension.ToolExposureModelOnly,
		PrepareLoadout: func(loadout extension.ToolLoadout) *extension.ToolLoadoutChanges {
			return prepareLoadout(loadout, options)
		},
		// Capable models write the script as raw text instead of a JSON-escaped string.
		ConstrainedSampling: constrainedSampling(),
		// The sandbox loads on the first call, not at startup.
		Execute: func(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			return Execute(ctx, toolCallID, params, onUpdate, options)
		},
		// upstream: tool.ts createCodemodeToolDefinition spreads codemodeRenderers into the definition.
		RenderCall:   codingagent.CodemodeRenderers.RenderCall,
		RenderResult: codingagent.CodemodeRenderers.RenderResult,
	}
}

func constrainedSampling() json.RawMessage {
	data, _ := json.Marshal(map[string]any{"type": "grammar", "variants": map[string]string{"openai_lark": sandbox.CodemodeSourceGrammar}})
	return data
}

// CreateCodemodeExtension is the factory of the `builtin:codemode` extension: it registers the tool inactive. Activate it with `--tools`,
// the `defaultTools` setting or `setActiveTools()`. The tool reads `codemode.mode` and `codemode.inlineBudget` through the API's
// GetSettings on every use, unless the options override them (upstream's readMode and readInlineBudget).
//
// Ports packages/coding-agent/src/extensions/codemode/index.ts (createCodemodeExtension).
func CreateCodemodeExtension(options Options) extension.ExtensionFactory {
	return func(pi extension.API) error {
		o := options
		o.Models = !o.DisableModels
		o.GetSettings = pi.GetSettings
		definition := Definition(o)
		inactive := false
		definition.DefaultActive = &inactive
		pi.RegisterTool(definition)
		return nil
	}
}
