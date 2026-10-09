// Package export provides HTML session export.
//
// Ports packages/coding-agent/src/core/export-html/index.ts using embedded pinned templates and vendor assets.
package export

import (
	"cmp"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/rpcclient"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
	"github.com/MichaelKinsy/PiG/tui"
)

const appName = "pig"

//go:embed assets/template.html assets/template.css assets/template.js assets/vendor/marked.min.js assets/vendor/highlight.min.js
var templateFS embed.FS

// SessionData mirrors upstream export-html's payload shape.
// Header and Entries use json.RawMessage to preserve the original JSONL
// key ordering. Go's map[string]any sorts keys alphabetically on Marshal,
// but upstream JS preserves insertion order. Using RawMessage ensures the
// base64-encoded session data in the HTML is byte-identical to upstream.
type SessionData struct {
	Header        json.RawMessage           `json:"header"`
	Entries       []json.RawMessage         `json:"entries"`
	LeafID        *string                   `json:"leafId"`
	SystemPrompt  *string                   `json:"systemPrompt,omitempty"`
	Tools         []ToolSchema              `json:"tools,omitzero"`
	RenderedTools map[string]map[string]any `json:"renderedTools,omitempty"`
}

// ToolSchema is one tool in the export's session data: upstream
// Pick<ToolDefinition, "name" | "description" | "parameters">.
type ToolSchema struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"`
}

// AgentState is the live agent state upstream exportSessionToHtml receives as
// its state argument: state.systemPrompt and state.tools.
type AgentState struct {
	SystemPrompt string
	Tools        []ToolSchema
}

// generateThemeVars is index.ts generateThemeVars: the resolved theme colors, then the export backgrounds.
func generateThemeVars(th *tui.Theme) string {
	colors := th.GetResolvedThemeColors()
	var lines []string
	for _, k := range th.ColorKeys() {
		lines = append(lines, fmt.Sprintf("--%s: %s;", k, colors[k]))
	}
	pageBg, cardBg, infoBg := exportBackgrounds(th, colors)
	lines = append(lines,
		fmt.Sprintf("--exportPageBg: %s;", pageBg),
		fmt.Sprintf("--exportCardBg: %s;", cardBg),
		fmt.Sprintf("--exportInfoBg: %s;", infoBg),
	)
	return strings.Join(lines, "\n      ")
}

// exportBackgrounds are the theme's explicit export colors, or the ones derived from its userMessageBg (index.ts:119-125).
func exportBackgrounds(th *tui.Theme, colors map[string]string) (pageBg, cardBg, infoBg string) {
	userMessageBg := colors["userMessageBg"]
	if userMessageBg == "" {
		userMessageBg = "#343541"
	}
	derived := deriveExportColors(userMessageBg)
	return cmp.Or(th.ExportPageBg, derived.pageBg), cmp.Or(th.ExportCardBg, derived.cardBg), cmp.Or(th.ExportInfoBg, derived.infoBg)
}

// ToHTML converts session data to Pi's self-contained HTML, with a base64 JSON.stringify payload and first-match template substitution.
func ToHTML(data SessionData) string {
	return toHTML(data, "")
}

// toHTML is index.ts generateHtml: ToHTML with the theme named themeName (the active theme when empty).
func toHTML(data SessionData, themeName string) string {
	template := mustReadAsset("assets/template.html")
	css := mustReadAsset("assets/template.css")
	js := mustReadAsset("assets/template.js")
	marked := mustReadAsset("assets/vendor/marked.min.js")
	highlight := mustReadAsset("assets/vendor/highlight.min.js")

	th := tui.ExportThemeNamed(themeName)
	bodyBg, containerBg, infoBg := exportBackgrounds(th, th.GetResolvedThemeColors())
	css = jsReplace(css, "{{THEME_VARS}}", generateThemeVars(th))
	css = jsReplace(css, "{{BODY_BG}}", bodyBg)
	css = jsReplace(css, "{{CONTAINER_BG}}", containerBg)
	css = jsReplace(css, "{{INFO_BG}}", infoBg)

	if data.Entries == nil {
		data.Entries = []json.RawMessage{}
	}
	payload, _ := rpcclient.SerializeJsonLine(data)
	sessionDataBase64 := base64.StdEncoding.EncodeToString([]byte(strings.TrimSuffix(string(payload), "\n")))

	// Upstream uses JavaScript's String.replace(search, replacement) which
	// interprets $& (→ matched substring) and $$ (→ literal "$") in the
	// replacement string. Go's strings.ReplaceAll is literal. Use jsReplace
	// to match upstream's byte-exact output.
	out := template
	out = jsReplace(out, "{{CSS}}", css)
	out = jsReplace(out, "{{JS}}", js)
	out = jsReplace(out, "{{SESSION_DATA}}", sessionDataBase64)
	out = jsReplace(out, "{{MARKED_JS}}", marked)
	out = jsReplace(out, "{{HIGHLIGHT_JS}}", highlight)
	return out
}

func mustReadAsset(path string) string {
	b, err := templateFS.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// jsReplace replicates JavaScript's String.prototype.replace(search, replacement)
// semantics for the $ special patterns in the replacement string:
//   - $$ → literal "$"
//   - $& → the matched search string
//   - $` → portion of the string before the match
//   - $' → portion of the string after the match
//
// This is needed because upstream's export-html/index.ts uses .replace()
// to inject vendor scripts (highlight.min.js contains $& in regex patterns,
// template.js uses $$ for literal $ in template literals). Without this,
// pig's output diverges from pi's byte-exact HTML.
func jsReplace(s, search, replacement string) string {
	before, after, ok := strings.Cut(s, search)
	if !ok {
		return s
	}
	// Only process $ patterns in the replacement if $ is present.
	if !strings.Contains(replacement, "$") {
		return before + replacement + after
	}
	// Process $ patterns in the replacement string.
	var b strings.Builder
	for i := 0; i < len(replacement); i++ {
		if replacement[i] == '$' && i+1 < len(replacement) {
			next := replacement[i+1]
			switch next {
			case '$':
				b.WriteByte('$')
				i++
				continue
			case '&':
				b.WriteString(search)
				i++
				continue
			case '`':
				// $` → portion before match (only for first occurrence)
				b.WriteString(before)
				i++
				continue
			case '\'':
				// $' → portion after match (only for first occurrence)
				b.WriteString(after)
				i++
				continue
			}
		}
		b.WriteByte(replacement[i])
	}
	return before + b.String() + after
}

// FromJSONL converts raw session JSONL bytes into export SessionData.
// Uses json.RawMessage to preserve the original key ordering from the JSONL.
func FromJSONL(data []byte) (SessionData, error) {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return SessionData{}, fmt.Errorf("empty session file")
	}
	var sd SessionData
	first := true
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Validate JSON before storing as RawMessage.
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			if first {
				return SessionData{}, fmt.Errorf("parse header: %w", err)
			}
			continue
		}
		if first {
			first = false
			if typ, _ := m["type"].(string); typ != "session" {
				return SessionData{}, fmt.Errorf("missing session header")
			}
			sd.Header = json.RawMessage(line)
			continue
		}
		sd.Entries = append(sd.Entries, json.RawMessage(line))
		if id, _ := m["id"].(string); id != "" {
			idCopy := id
			sd.LeafID = &idCopy
		}
	}
	if first {
		return SessionData{}, fmt.Errorf("empty session file")
	}
	return sd, nil
}

// ExportFromFile reads a session JSONL file and writes the upstream-style HTML export.
func ExportFromFile(inputPath, outputPath string) (string, error) {
	return ExportFromFileWithTools(inputPath, outputPath, nil, "", nil, "")
}

// ExportFromFileWithTools is ExportFromFile with tool calls and results drawn
// through the renderers getToolRenderers returns, as upstream
// AgentSession.exportToHtml passes a tool renderer. A non-nil state
// is the live agent state upstream passes to exportSessionToHtml, which embeds
// its systemPrompt and tools; nil is upstream exportFromFile (CLI --export),
// whose session data carries neither. themeName is the export theme (opts.themeName); empty exports with the active theme.
func ExportFromFileWithTools(inputPath, outputPath string, getToolRenderers func(name string) *extension.ToolRenderers, cwd string, state *AgentState, themeName string) (string, error) {
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return "", fmt.Errorf("read session: %w", err)
	}
	sd, err := FromJSONL(data)
	if err != nil {
		return "", fmt.Errorf("parse session: %w", err)
	}
	if state != nil {
		sd.SystemPrompt = &state.SystemPrompt
		// upstream state.tools.map(...) is an array even when no tool is active.
		sd.Tools = append([]ToolSchema{}, state.Tools...)
	}
	RenderCustomTools(&sd, getToolRenderers, cwd, 100)
	htmlStr := toHTML(sd, themeName)
	if outputPath == "" {
		base := strings.TrimSuffix(filepath.Base(inputPath), ".jsonl")
		outputPath = fmt.Sprintf("%s-session-%s.html", appName, base)
	}
	if err := os.WriteFile(outputPath, []byte(htmlStr), 0o644); err != nil {
		// writeFileSync's error: "ENOENT: no such file or directory, open '<path>'".
		return "", nodeerrno.FromPathError(err)
	}
	return outputPath, nil
}

// WriteHTML writes the export of data to outputPath as exportFromFile does (index.ts: generateHtml, then writeFileSync), or to
// "<app>-session-<basename of inputPath without .jsonl>.html" in the working directory when outputPath is empty. It returns the path written.
func WriteHTML(data SessionData, inputPath, outputPath string) (string, error) {
	htmlStr := toHTML(data, "")
	if outputPath == "" {
		outputPath = fmt.Sprintf("%s-session-%s.html", appName, strings.TrimSuffix(filepath.Base(inputPath), ".jsonl"))
	}
	if err := os.WriteFile(outputPath, []byte(htmlStr), 0o644); err != nil {
		return "", nodeerrno.FromPathError(err)
	}
	return outputPath, nil
}

// exportColors are the page, card and info backgrounds of an export (index.ts deriveExportColors).
type exportColors struct{ pageBg, cardBg, infoBg string }

// jsSpace is JavaScript's \s, which RE2's ASCII-only \s is narrower than.
const jsSpace = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

var (
	hexColorPattern = lazyregexp.New(`^#([0-9a-fA-F]{2})([0-9a-fA-F]{2})([0-9a-fA-F]{2})$`)
	rgbColorPattern = lazyregexp.New(`^rgb` + jsSpace + `*\(` + jsSpace + `*(\d+)` + jsSpace + `*,` + jsSpace + `*(\d+)` + jsSpace + `*,` + jsSpace + `*(\d+)` + jsSpace + `*\)$`)
)

type rgbChannels struct{ r, g, b float64 }

// parseExportColor is index.ts parseColor: #RRGGBB and rgb(r,g,b) only. Channels are float64 because JavaScript numbers do not overflow.
func parseExportColor(color string) (rgbChannels, bool) {
	if m := hexColorPattern.FindStringSubmatch(color); m != nil {
		var c [3]float64
		for i := range c {
			v, _ := strconv.ParseUint(m[i+1], 16, 8)
			c[i] = float64(v)
		}
		return rgbChannels{c[0], c[1], c[2]}, true
	}
	if m := rgbColorPattern.FindStringSubmatch(color); m != nil {
		var c [3]float64
		for i := range c {
			c[i], _ = strconv.ParseFloat(m[i+1], 64)
		}
		return rgbChannels{c[0], c[1], c[2]}, true
	}
	return rgbChannels{}, false
}

// exportLuminance is index.ts getLuminance.
func exportLuminance(c rgbChannels) float64 {
	toLinear := func(v float64) float64 {
		s := v / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	// The explicit conversions keep each product rounded before the sum, as JavaScript computes it; arm64 would otherwise fuse them into one multiply-add.
	return float64(0.2126*toLinear(c.r)) + float64(0.7152*toLinear(c.g)) + float64(0.0722*toLinear(c.b))
}

// jsRound is Math.round: halves round toward positive infinity.
func jsRound(v float64) float64 {
	f := math.Floor(v)
	if v-f >= 0.5 {
		return f + 1
	}
	return f
}

// rgbCSS is index.ts's `rgb(${r}, ${g}, ${b})` template: each channel is JavaScript's Number to string, which writes an unclamped channel of 1e21 or more in exponent form.
func rgbCSS(r, g, b float64) string {
	return "rgb(" + jsnumber.String(r) + ", " + jsnumber.String(g) + ", " + jsnumber.String(b) + ")"
}

// adjustBrightness is index.ts adjustBrightness: a color it cannot parse is returned unchanged.
func adjustBrightness(color string, factor float64) string {
	parsed, ok := parseExportColor(color)
	if !ok {
		return color
	}
	// float64() keeps the product rounded before jsRound subtracts the floor, as JavaScript does; arm64 would otherwise fuse the product into that subtraction.
	adjust := func(c float64) float64 { return math.Min(255, math.Max(0, jsRound(float64(c*factor)))) }
	return rgbCSS(adjust(parsed.r), adjust(parsed.g), adjust(parsed.b))
}

// deriveExportColors is index.ts deriveExportColors: export backgrounds derived from a base color such as userMessageBg.
func deriveExportColors(baseColor string) exportColors {
	parsed, ok := parseExportColor(baseColor)
	if !ok {
		return exportColors{"rgb(24, 24, 30)", "rgb(30, 30, 36)", "rgb(60, 55, 40)"}
	}
	if exportLuminance(parsed) > 0.5 {
		return exportColors{
			pageBg: adjustBrightness(baseColor, 0.96),
			cardBg: baseColor,
			infoBg: rgbCSS(math.Min(255, parsed.r+10), math.Min(255, parsed.g+5), math.Max(0, parsed.b-20)),
		}
	}
	return exportColors{
		pageBg: adjustBrightness(baseColor, 0.7),
		cardBg: adjustBrightness(baseColor, 0.85),
		infoBg: rgbCSS(math.Min(255, parsed.r+20), math.Min(255, parsed.g+15), parsed.b),
	}
}
