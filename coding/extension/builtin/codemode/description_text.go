package codemode

import "strings"

// The model-facing text of the tool, copied from packages/coding-agent/src/extensions/codemode/tool.ts.

// descriptionIntro is DESCRIPTION_INTRO.
const descriptionIntro = "Run JavaScript that calls other tools. The input is raw JavaScript (not JSON, no code fence), run as an async function body in a QuickJS sandbox: top-level `await` and `return` work. No Node, file system, network, or timers.\n" +
	"- `await tools.<name>({ ...args })` resolves to a string, or an object if the tool's declaration says so, and rejects with an Error on failure. Calls still running when the script ends are cancelled.\n" +
	"- Optional first line: `// @options: {\"max_output_tokens\": 10000, \"timeout_ms\": 60000}`"

// describeGlobals lists the script globals, one line each. The details live in DocsPath.
//
// Ports packages/coding-agent/src/extensions/codemode/tool.ts (describeGlobals).
func describeGlobals(models bool) string {
	lines := []string{
		"Globals:",
		"- `text(value)`, `image(dataUrlOrImageBlock)`, `console.log(...)`, and top-level `return` add output; `exit()` ends the script. With several text items, each starts with a `==> text N/M <==` line, and `console` lines follow the other output in one `<console_output>` block. `image()` also saves the image to a temp file and the result names its path.",
		"- `store(key, value)` and `load(key)` keep JSON values across codemode calls.",
		"- `ALL_TOOLS`, `await searchTools(query, { limit?, namespace? })`, `await describeTool(name)`, `await describeNamespace(name)`: find unlisted tools, such as MCP tools.",
	}
	if models {
		lines = append(lines, "- `models`: classifiers and image generation."+docsCitation(" Read %s first."))
	}
	return strings.Join(lines, "\n")
}
