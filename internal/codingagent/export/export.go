// Package export provides HTML session export.
//
// Ports upstream export-html/ (index.ts + templates + vendor assets) using
// embedded upstream template files and pig session-data serialization.
package export

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

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
	SystemPrompt  string                    `json:"systemPrompt,omitempty"`
	Tools         []map[string]any          `json:"tools,omitempty"`
	RenderedTools map[string]map[string]any `json:"renderedTools,omitempty"`
}

// defaultTextColor is the fallback for empty-string color tokens in the
// dark theme. Matches upstream getResolvedThemeColors() which substitutes
// "" → defaultText ("#e5e5e7" for dark, "#000000" for light).
const defaultTextColorDark = "#e5e5e7"
const defaultTextColorLight = "#000000"

func generateThemeVars() string {
	th := tui.ActiveTheme()
	if th == nil {
		return ""
	}
	colors := th.Colors()
	if colors == nil {
		return ""
	}
	// Use ColorKeys() to preserve upstream JSON insertion order.
	// Fall back to sorted keys if colorKeys is not available.
	keys := th.ColorKeys()
	if len(keys) == 0 {
		keys = make([]string, 0, len(colors))
		for k := range colors {
			keys = append(keys, k)
		}
		slices.Sort(keys)
	}
	// Upstream getResolvedThemeColors fills empty strings with the default
	// text color for the theme variant (dark=#e5e5e7, light=#000000).
	defaultText := defaultTextColorDark
	if th.Name == "light" {
		defaultText = defaultTextColorLight
	}
	var lines []string
	for _, k := range keys {
		v := colors[k]
		if v == "" {
			v = defaultText
		}
		lines = append(lines, fmt.Sprintf("--%s: %s;", k, v))
	}
	pageBg := th.ExportPageBg
	if pageBg == "" {
		pageBg = "#18181e"
	}
	cardBg := th.ExportCardBg
	if cardBg == "" {
		cardBg = "#1e1e24"
	}
	infoBg := th.ExportInfoBg
	if infoBg == "" {
		infoBg = "#3c3728"
	}
	lines = append(lines,
		fmt.Sprintf("--exportPageBg: %s;", pageBg),
		fmt.Sprintf("--exportCardBg: %s;", cardBg),
		fmt.Sprintf("--exportInfoBg: %s;", infoBg),
	)
	return strings.Join(lines, "\n      ")
}

// ToHTML converts session data to the upstream-style self-contained SPA HTML.
func ToHTML(data SessionData) string {
	template := mustReadAsset("assets/template.html")
	css := mustReadAsset("assets/template.css")
	js := mustReadAsset("assets/template.js")
	marked := mustReadAsset("assets/vendor/marked.min.js")
	highlight := mustReadAsset("assets/vendor/highlight.min.js")

	th := tui.ActiveTheme()
	bodyBg, containerBg, infoBg := "#18181e", "#1e1e24", "#3c3728"
	if th != nil {
		if th.ExportPageBg != "" {
			bodyBg = th.ExportPageBg
		}
		if th.ExportCardBg != "" {
			containerBg = th.ExportCardBg
		}
		if th.ExportInfoBg != "" {
			infoBg = th.ExportInfoBg
		}
	}
	css = strings.ReplaceAll(css, "{{THEME_VARS}}", generateThemeVars())
	css = strings.ReplaceAll(css, "{{BODY_BG}}", bodyBg)
	css = strings.ReplaceAll(css, "{{CONTAINER_BG}}", containerBg)
	css = strings.ReplaceAll(css, "{{INFO_BG}}", infoBg)

	payload, _ := json.Marshal(data)
	sessionDataBase64 := base64.StdEncoding.EncodeToString(payload)

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
		return strings.ReplaceAll(s, search, replacement)
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
	processed := b.String()
	return strings.ReplaceAll(s, search, processed)
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
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return "", fmt.Errorf("read session: %w", err)
	}
	sd, err := FromJSONL(data)
	if err != nil {
		return "", fmt.Errorf("parse session: %w", err)
	}
	htmlStr := ToHTML(sd)
	if outputPath == "" {
		base := strings.TrimSuffix(filepath.Base(inputPath), ".jsonl")
		outputPath = fmt.Sprintf("%s-session-%s.html", appName, base)
	}
	if err := os.WriteFile(outputPath, []byte(htmlStr), 0o644); err != nil {
		return "", fmt.Errorf("write html: %w", err)
	}
	return outputPath, nil
}
