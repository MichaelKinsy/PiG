package toolsearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// inputSchema is upstream's toolSearchSchema.
var inputSchema = json.RawMessage(`{"type":"object","properties":{"query":{"description":"Search query for deferred tools.","type":"string"},"limit":{"description":"Maximum number of tools to return. Defaults to 8.","type":"number"}},"required":["query"]}`)

// Details are the details of a tool_search result.
type Details struct {
	// Loaded lists the tools this call loaded.
	Loaded []string `json:"loaded"`
}

// isSearchable reports whether tool_search can load a tool with this exposure.
func isSearchable(exposure extension.ToolExposure) bool {
	return exposure == extension.ToolExposureCodemode || exposure == extension.ToolExposureDeferred
}

type resultTool struct{ name, description string }

// searchAndLoad ranks the searchable tools that are not active yet and activates the matches, so the next model call
// declares them. Activation is recorded in the transcript like any tool change.
func searchAndLoad(tc *extension.Context, query string, limit int) []resultTool {
	active := tc.GetActiveTools()
	var candidates []extension.ToolInfo
	for _, tool := range tc.GetAllTools() {
		if isSearchable(tool.Exposure) && !slices.Contains(active, tool.Name) {
			candidates = append(candidates, tool)
		}
	}
	documents := make([]Document, len(candidates))
	for i, tool := range candidates {
		documents[i] = CreateDocument(tool, tool.Namespace)
	}
	matches := NewBm25Ranker().Rank(query, documents, limit)
	if len(matches) > 0 {
		next := slices.Clone(active)
		for _, match := range matches {
			next = append(next, match.Name)
		}
		tc.SetActiveTools(next)
	}
	found := make([]resultTool, len(matches))
	for i, match := range matches {
		found[i].name = match.Name
		for _, candidate := range candidates {
			if candidate.Name == match.Name {
				found[i].description = candidate.Description
				break
			}
		}
	}
	return found
}

// Definition is the tool definition of `tool_search`, without the registration flag: Extension registers it inactive.
//
// Ports packages/coding-agent/src/extensions/tool-search/tool.ts (createToolSearchToolDefinition).
func Definition() extension.ToolDefinition {
	return extension.ToolDefinition{
		Name:          ToolName,
		Label:         ToolName,
		Description:   ToolSearchDescription,
		PromptSnippet: "Search for tools that are not loaded yet and load the matches",
		Parameters:    inputSchema,
		// Searching is not something scripts need; it changes what the model sees.
		Exposure: extension.ToolExposureModelOnly,
		Execute: func(ctx context.Context, _ string, params json.RawMessage, _ extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
			var input struct {
				Query string   `json:"query"`
				Limit *float64 `json:"limit"`
			}
			if err := json.Unmarshal(params, &input); err != nil {
				return nil, fmt.Errorf("invalid tool_search input: %w", err)
			}
			if jsstring.Trim(input.Query) == "" {
				return nil, errors.New("query must not be empty")
			}
			limit := float64(DefaultLimit)
			if input.Limit != nil {
				limit = *input.Limit
			}
			if limit != math.Trunc(limit) || limit <= 0 {
				return nil, errors.New("limit must be a positive integer")
			}
			var found []resultTool
			if tc := extension.FromContext(ctx); tc != nil {
				found = searchAndLoad(tc, input.Query, LimitOf(limit))
			}
			text := "No matching tools found."
			if len(found) > 0 {
				lines := make([]string, len(found))
				for i, tool := range found {
					first, _, _ := strings.Cut(strings.ReplaceAll(jsstring.Trim(tool.description), "\r\n", "\n"), "\n")
					lines[i] = "- " + tool.name + ": " + first
				}
				plural := "s"
				if len(found) == 1 {
					plural = ""
				}
				text = fmt.Sprintf("Loaded %d tool%s. They are available from your next call:\n%s", len(found), plural, strings.Join(lines, "\n"))
			}
			loaded := make([]string, len(found))
			for i, tool := range found {
				loaded[i] = tool.name
			}
			return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, Details: Details{Loaded: loaded}}, nil
		},
	}
}

// Extension is the `builtin:tool-search` extension: the tool registered inactive. Activate it with `--tools`, the
// `defaultTools` setting or `setActiveTools()`.
//
// Ports packages/coding-agent/src/extensions/tool-search/index.ts (createToolSearchExtension).
func Extension() (extension.Extension, error) {
	definition := Definition()
	inactive := false
	definition.DefaultActive = &inactive
	return extension.Extension{
		Tools:     map[string]extension.RegisteredTool{ToolName: {Definition: definition}},
		ToolOrder: []string{ToolName},
	}, nil
}
