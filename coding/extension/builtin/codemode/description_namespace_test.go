package codemode_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/builtin/codemode"
)

// tool.ts DESCRIPTION_INTRO and describeGlobals of 1.0.0, which the catalog cases of tool-search.test.ts only match in
// part. Without nested tools the description is the intro and the globals, one line per global; the line that names
// describeNamespace() and the search guidance for tools that are not listed are always there.
const descriptionIntro = "Run JavaScript that calls other tools. The input is raw JavaScript (not JSON, no code fence), run as an async function body in a QuickJS sandbox: top-level `await` and `return` work. No Node, file system, network, or timers.\n" +
	"- `await tools.<name>({ ...args })` resolves to a string, or an object if the tool's declaration says so, and rejects with an Error on failure. Calls still running when the script ends are cancelled.\n" +
	"- Optional first line: `// @options: {\"max_output_tokens\": 10000, \"timeout_ms\": 60000}`"

const describeGlobals = "Globals:\n" +
	"- `text(value)`, `image(dataUrlOrImageBlock)`, `console.log(...)`, and top-level `return` add output; `exit()` ends the script. `image()` also saves the image to a temp file and the result names its path.\n" +
	"- `store(key, value)` and `load(key)` keep JSON values across codemode calls.\n" +
	"- `ALL_TOOLS`, `searchTools(query, { limit?, namespace? })`, `describeTool(name)`, `describeNamespace(name)`: find unlisted tools, such as MCP tools."

func TestCodemodeDescriptionDocumentsDescribeNamespaceAndAlwaysCarriesTheSearchGuidance(t *testing.T) {
	if got, want := codemode.CreateDescription(nil, codemode.DescriptionOptions{}), descriptionIntro+"\n\n"+describeGlobals; got != want {
		t.Errorf("description:\n%q\nwant\n%q", got, want)
	}
}

// describeGlobals(true) adds one line for `models` that points to CODEMODE_DOCS_PATH instead of declaring the API.
func TestCodemodeDescriptionNamesTheModelsGlobalInOneLine(t *testing.T) {
	docs := codemode.DocsPath()
	if docs == "" {
		t.Error("DocsPath() is empty; upstream CODEMODE_DOCS_PATH is join(getDocsPath(), \"codemode.md\")")
	}
	want := descriptionIntro + "\n\n" + describeGlobals + "\n- `models`: classifiers and image generation. Read " + docs + " first."
	if got := codemode.CreateDescription(nil, codemode.DescriptionOptions{Models: true}); got != want {
		t.Errorf("description:\n%q\nwant\n%q", got, want)
	}
}

// tool.ts codemodeToolSystemPromptContribution and codemodeSchema of 1.0.0: the shorter prompt snippet, guideline and
// `code` parameter description.
func TestCodemodePromptContributionAndSchemaAreTheLeanerOnes(t *testing.T) {
	definition := codemode.Definition(codemode.Options{})
	if want := "Run JavaScript that calls other tools"; definition.PromptSnippet != want {
		t.Errorf("PromptSnippet = %q, want %q", definition.PromptSnippet, want)
	}
	if want := []string{"Use codemode to batch independent tool calls (Promise.allSettled), chain them, or filter large output, instead of many separate calls."}; !reflect.DeepEqual(definition.PromptGuidelines, want) {
		t.Errorf("PromptGuidelines = %q, want %q", definition.PromptGuidelines, want)
	}
	var schema struct {
		Properties struct {
			Code struct {
				Description string `json:"description"`
				Type        string `json:"type"`
			} `json:"code"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(definition.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Properties.Code.Description != "Raw JavaScript source." || schema.Properties.Code.Type != "string" || !reflect.DeepEqual(schema.Required, []string{"code"}) {
		t.Errorf("parameters = %s", definition.Parameters)
	}
}
