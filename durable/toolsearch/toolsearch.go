// Ports packages/durable/src/tool-search/tool-search.ts and packages/durable/src/tool-search/index.ts.

// Package toolsearch is the `tool_search` tool, a copy of the coding agent's: the model searches the deferred tools it is not
// offered yet, and the matches are loaded with the addTools control, so the next request offers them. Loading writes the
// conversation's modelTools (its add), so loaded tools stay offered on that branch, also after a compaction.
package toolsearch

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/internal/search"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// ToolName is the name of the tool_search tool.
const ToolName = "tool_search"

// DefaultLimit is the number of tools tool_search loads at most when the model gives no limit.
const DefaultLimit = 8

// description is fixed, so it does not change while deferred tools are registered, for example as MCP servers connect.
const description = "# Tool discovery\n\nSearches over deferred tool metadata with BM25 and exposes matching tools for the next model call.\n\nSome of the tools, such as tools of MCP servers, may not have been provided to you upfront, and you should use this tool (`" + ToolName + "`) to search for the required tools. For MCP tool discovery, always use `" + ToolName + "`."

const parameters = `{"type":"object","properties":{"query":{"type":"string","description":"Search query for deferred tools."},"limit":{"type":"number","description":"Maximum number of tools to return. Defaults to 8."}},"required":["query"]}`

// Details are the details of a tool_search result: the tools it loaded.
type Details struct {
	Loaded []string `json:"loaded"`
}

// schemaText appends the descriptions and property names of a schema, recursively.
func schemaText(schema any, parts *[]string) {
	object, ok := schema.(map[string]any)
	if !ok {
		return
	}
	if text, ok := object["description"].(string); ok {
		*parts = append(*parts, text)
	}
	if properties, ok := object["properties"].(map[string]any); ok {
		names := make([]string, 0, len(properties))
		for name := range properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			*parts = append(*parts, name)
			schemaText(properties[name], parts)
		}
	}
	schemaText(object["items"], parts)
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if variants, ok := object[key].([]any); ok {
			for _, variant := range variants {
				schemaText(variant, parts)
			}
		}
	}
}

// searchText is the search text of a tool: its name, the name with `_` as spaces, its description, and its parameters' text.
func searchText(tool *durable.ToolRegistration) string {
	parts := []string{tool.Name, strings.ReplaceAll(tool.Name, "_", " "), tool.Description}
	var decoded any
	//portlint:allow mapkeyorder the text only feeds BM25 (search.RankDocuments), whose term counts and lengths ignore the order of the parts
	if encoded, err := json.Marshal(tool.Parameters); err == nil {
		//portlint:allow jsonclone once per tool per tool_search call, which is a model turn; the parameters are walked as generic JSON
		_ = json.Unmarshal(encoded, &decoded) //perf:ok once per tool per tool_search call, which is a model turn; the parameters are walked as generic JSON
	}
	schemaText(decoded, &parts)
	kept := parts[:0]
	for _, part := range parts {
		if jsstring.Trim(part) != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, " ")
}

// firstLine is the first line of a trimmed description.
func firstLine(text string) string {
	line, _, _ := strings.Cut(jsstring.Trim(text), "\n")
	return strings.TrimSuffix(line, "\r")
}

// CreateTool creates the tool_search tool.
func CreateTool() *durable.ToolRegistration {
	encoded, err := json.Marshal(struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}{ToolName, description, json.RawMessage(parameters)})
	if err != nil {
		panic(err)
	}
	var schema ai.ToolSchema
	//portlint:allow jsonclone builds the tool schema once at tool construction
	if err := json.Unmarshal(encoded, &schema); err != nil { //perf:ok builds the tool schema once at tool construction
		panic(err)
	}
	return harness.DefineTool(durable.ToolRegistration{
		ToolSchema: schema,
		// Searching changes what the model is offered; scripts have their own searchTools().
		Callers: []durable.ToolCaller{durable.CallerModel},
		Replay:  durable.ReplaySafe,
		Execute: func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			input, err := durable.FromJsonValue[struct {
				Query string   `json:"query"`
				Limit *float64 `json:"limit"`
			}](args)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			if jsstring.Trim(input.Query) == "" {
				return durable.ToolExecutionResult{}, errors.New("query must not be empty")
			}
			limit := float64(DefaultLimit)
			if input.Limit != nil {
				limit = *input.Limit
			}
			if limit != math.Trunc(limit) || limit <= 0 {
				return durable.ToolExecutionResult{}, errors.New("limit must be a positive integer")
			}
			agent, err := api.Agent(ctx)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			candidates := agent.Deferred
			documents := make([]search.Document, len(candidates))
			for i, tool := range candidates {
				documents[i] = search.Document{Name: tool.Name, Text: searchText(tool)}
			}
			//portlint:allow numbers limit is a positive integer decoded from JSON, so finite; rankDocuments' slice(0, limit) keeps every document past MaxInt32
			loaded := search.RankDocuments(input.Query, documents, int(min(limit, math.MaxInt32)))
			text := "No matching tools found."
			if len(loaded) > 0 {
				plural := "s"
				if len(loaded) == 1 {
					plural = ""
				}
				lines := make([]string, len(loaded))
				for i, name := range loaded {
					described := ""
					if index := slices.IndexFunc(candidates, func(tool *durable.ToolRegistration) bool { return tool.Name == name }); index >= 0 {
						described = candidates[index].Description
					}
					lines[i] = "- " + name + ": " + firstLine(described)
				}
				text = "Loaded " + strconv.Itoa(len(loaded)) + " tool" + plural + ". They are available from your next call:\n" + strings.Join(lines, "\n")
			}
			names := make([]any, len(loaded))
			for i, name := range loaded {
				names[i] = name
			}
			result := durable.ToolExecutionResult{
				Output:     []ai.ToolResultMessageContent{ai.TextContent{Text: text}},
				Details:    map[string]any{"loaded": names},
				HasDetails: true,
			}
			if len(loaded) > 0 {
				result.Control = &durable.ToolControl{AddTools: loaded}
			}
			return result, nil
		},
	})
}

// CreateExtension creates the tool_search extension: install it and select it where the model should find deferred tools.
func CreateExtension() *durable.Extension {
	return &durable.Extension{Name: "tool-search", Tools: []*durable.ToolRegistration{CreateTool()}}
}
