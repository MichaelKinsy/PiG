package cli

import (
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// pig additive (D92): stripHelp drops or rewrites the --help entries of the built-ins this process strips, so the help
// names no tool, built-in extension, provider or feature a Piglet left out. Stock PiG strips nothing and prints Pi's
// text unchanged.
func stripHelp(text string) string {
	if !pigstrip.Active() {
		return text
	}
	sections := strings.SplitAfter(text, "\n\n")
	var out strings.Builder
	for i, section := range sections {
		lines := strings.SplitAfter(section, "\n")
		var kept strings.Builder
		dropExample := false
		for j, line := range lines {
			if i == 0 && j == 0 {
				line = stripHelpTitle(line)
			}
			rewritten, keep := stripHelpLine(line)
			if !keep && strings.HasPrefix(section, "  # ") {
				// An example whose command uses a stripped built-in goes whole, its comment included.
				dropExample = true
				break
			}
			if keep {
				kept.WriteString(rewritten)
			}
		}
		if !dropExample {
			out.WriteString(kept.String())
		}
	}
	return out.String()
}

// stripHelpTitle rewrites the title's list of default tools without the stripped ones.
func stripHelpTitle(line string) string {
	const prefix, suffix = "pig - AI coding assistant with ", " tools\n"
	list, ok := strings.CutPrefix(line, prefix)
	if !ok || !strings.HasSuffix(list, suffix) {
		return line
	}
	var tools []string
	for tool := range strings.SplitSeq(strings.TrimSuffix(list, suffix), ", ") {
		if !pigstrip.Has(pigstrip.ListTools, tool) {
			tools = append(tools, tool)
		}
	}
	if len(tools) == 0 {
		return "pig - AI coding assistant\n"
	}
	return prefix + strings.Join(tools, ", ") + suffix
}

// stripHelpLine returns line rewritten for the strip, or false when its entry belongs to a stripped built-in.
func stripHelpLine(line string) (string, bool) {
	feature := func(id string) bool { return pigstrip.Has(pigstrip.ListFeatures, id) }
	mcpStripped := pigstrip.Has(pigstrip.ListExtensions, "mcp")
	trimmed := strings.TrimLeft(line, " ")
	name, _, _ := strings.Cut(trimmed, " ")
	switch {
	case strings.HasPrefix(line, "  pig mcp "):
		return line, !pigstrip.Has(pigstrip.ListExtensions, "mcp")
	case strings.HasPrefix(line, "  pig <command> --help ") && mcpStripped:
		return strings.Replace(line, "/auth/mcp\n", "/auth\n", 1), true
	case name == "--no-mcp" || trimmed == "Keeps MCP tools unless an entry starts with mcp__\n":
		return line, !mcpStripped
	case trimmed == "Applies to all tools, MCP tools included\n" && mcpStripped:
		return strings.Replace(line, ", MCP tools included\n", "\n", 1), true
	case strings.HasPrefix(line, "  pig update [source|self|pig] ") && feature(pigstrip.SelfUpdate):
		return "  pig update [source]           Update extensions or model catalogs\n", true
	case strings.HasPrefix(line, "  pig docs "):
		return line, !feature(pigstrip.Docs)
	case strings.HasPrefix(line, "  pig piglet <command> ") && feature(pigstrip.PigletBuilder):
		return strings.Replace(line, "List, show, validate, and build Piglets", "List, show, and validate Piglets", 1), true
	case strings.HasPrefix(line, "  pig extension init "):
		return stripHelpExtensionInit(line)
	case name == "--theme" || name == "--no-themes":
		return line, !feature(pigstrip.Themes)
	case name == "--skill" || name == "--no-skills,":
		return line, !feature(pigstrip.Skills)
	case name == "--prompt-template" || name == "--no-prompt-templates,":
		return line, !feature(pigstrip.PromptTemplates)
	case name == "--export" || strings.HasPrefix(trimmed, "pig --export "):
		return line, !feature(pigstrip.ExportHTML)
	case strings.HasPrefix(trimmed, "pig --tools "):
		tools, _, _ := strings.Cut(strings.TrimPrefix(trimmed, "pig --tools "), " ")
		for tool := range strings.SplitSeq(tools, ",") {
			tool = strings.Trim(tool, "'\n")
			if pigstrip.Has(pigstrip.ListTools, tool) || pigstrip.Has(pigstrip.ListExtensions, tool) || mcpStripped && strings.HasPrefix(tool, "mcp__") {
				return line, false
			}
		}
	case strings.Contains(line, " - ") && pigstrip.Has(pigstrip.ListTools, name):
		return line, false
	case strings.Contains(line, " - ") && helpEnvProviderStripped(name):
		return line, false
	}
	return line, true
}

// stripHelpExtensionInit lists only the extension languages whose SDK this process has.
func stripHelpExtensionInit(line string) (string, bool) {
	const all = "Create a Go, Python, or Rust extension"
	var languages []string
	for _, sdk := range []struct{ id, language string }{
		{pigstrip.ExtensionSDKGo, "Go"}, {pigstrip.ExtensionSDKPython, "Python"}, {pigstrip.ExtensionSDKRust, "Rust"},
	} {
		if !pigstrip.Has(pigstrip.ListFeatures, sdk.id) {
			languages = append(languages, sdk.language)
		}
	}
	switch len(languages) {
	case 0:
		return line, false
	case 1:
		return strings.Replace(line, all, "Create a "+languages[0]+" extension", 1), true
	case 2:
		return strings.Replace(line, all, "Create a "+languages[0]+" or "+languages[1]+" extension", 1), true
	}
	return line, true
}

// helpEnvProviderStripped reports whether the environment variable a help row names belongs to a built-in provider
// that offers no model under the strip.
func helpEnvProviderStripped(name string) bool {
	// upstream: packages/ai/src/env-api-keys.ts:getEnvApiKey special-cases amazon-bedrock with its AWS_ credential chain, and
	// packages/coding-agent/src/cli/args.ts lists the AWS_ rows (AWS_REGION included) as Amazon Bedrock's.
	if strings.HasPrefix(name, "AWS_") {
		return ai.ProviderStripped("amazon-bedrock")
	}
	// FindEnvKeys reports the provider's variables that hold a value; giving name one asks whether it is one of them,
	// whatever the shell sets.
	probe := map[string]string{name: name}
	for _, provider := range ai.ListRuntimeProviders() {
		if ai.ProviderStripped(provider) && slices.Contains(ai.FindEnvKeys(provider, probe), name) {
			return true
		}
	}
	return false
}
