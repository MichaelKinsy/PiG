// Package parity parses upstream pi TypeScript declarations to drive
// parity tests. It extracts the ExtensionAPI surface (events + methods) and
// every Event / EventResult interface from
// .upstream/current/packages/coding-agent/src/core/extensions/types.ts.
//
// The parser is regex-based and intentionally narrow. It does not implement a
// general TypeScript grammar: it relies on the upstream file using a
// consistent style (single-line `on(event: "...")` overloads, top-level
// `export interface XxxEvent {` blocks, etc.). If upstream restructures the
// file in a way that breaks the parser, the parser tests will fail loudly.
package parity

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

// ─── Public API surface ──────────────────────────────────────────────────────

// ExtensionAPISurface is the parsed shape of upstream's ExtensionAPI interface.
type ExtensionAPISurface struct {
	// Events is the list of event names registrable via `on(event: "...")`,
	// in declaration order, lower_snake_case as they appear in upstream.
	Events []string

	// Methods is the list of method names on ExtensionAPI other than `on`,
	// in declaration order, camelCase as they appear in upstream.
	Methods []string
}

// EventTypeDecl is the parsed shape of one `export interface XxxEvent {…}` or
// `XxxEventResult {…}` block.
type EventTypeDecl struct {
	Name   string            // e.g. "ToolCallEvent"
	Fields map[string]string // fieldName -> raw TS type string
	// FieldOrder preserves declaration order (maps don't).
	FieldOrder []string
}

// UpstreamSurface is the full parsed snapshot.
type UpstreamSurface struct {
	API          ExtensionAPISurface
	EventTypes   []EventTypeDecl // *Event interfaces (excluding *EventResult)
	EventResults []EventTypeDecl // *EventResult interfaces
}

// ─── File discovery ──────────────────────────────────────────────────────────

// upstreamTypesPath returns the absolute path to upstream's types.ts.
// It walks up from this source file's directory to the repo root and joins
// the well-known path. This works whether tests run from the package dir or
// the repo root.
func upstreamTypesPath() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("parity: cannot determine caller path")
	}
	// thisFile = .../pig/tests/upstream-parity/parser.go
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	abs, err := filepath.Abs(filepath.Join(repoRoot,
		".upstream", "current",
		"packages", "coding-agent", "src", "core", "extensions", "types.ts",
	))
	if err != nil {
		return "", fmt.Errorf("parity: abs: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("parity: types.ts not found at %s; run automation/gen/mirror-upstream.sh: %w", abs, err)
	}
	return abs, nil
}

// LoadUpstream parses the upstream types.ts at the well-known path.
func LoadUpstream() (*UpstreamSurface, error) {
	path, err := upstreamTypesPath()
	if err != nil {
		return nil, err
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("parity: read %s: %w", path, err)
	}
	return parseSource(string(src))
}

// LoadUpstreamFrom parses TypeScript source from an explicit string.
// Used by parser self-tests against fixture inputs.
func LoadUpstreamFrom(src string) (*UpstreamSurface, error) {
	return parseSource(src)
}

// ─── Parser ─────────────────────────────────────────────────────────────────

func parseSource(src string) (*UpstreamSurface, error) {
	out := &UpstreamSurface{}

	apiBlock, err := extractInterfaceBlock(src, "ExtensionAPI")
	if err != nil {
		return nil, fmt.Errorf("parity: ExtensionAPI: %w", err)
	}
	out.API = parseExtensionAPI(apiBlock)

	for _, decl := range extractEventInterfaces(src) {
		if strings.HasSuffix(decl.Name, "EventResult") {
			out.EventResults = append(out.EventResults, decl)
		} else {
			out.EventTypes = append(out.EventTypes, decl)
		}
	}
	return out, nil
}

// extractInterfaceBlock returns the body of `export interface <name> {…}`,
// brace-balanced. Returns the inside of the outermost braces (no opening or
// closing brace).
func extractInterfaceBlock(src, name string) (string, error) {
	startRE := regexp.MustCompile(`(?m)^export interface ` + regexp.QuoteMeta(name) + `\b[^{]*\{`)
	loc := startRE.FindStringIndex(src)
	if loc == nil {
		return "", fmt.Errorf("interface %q not found", name)
	}
	body, err := readBracedBlock(src[loc[1]-1:])
	if err != nil {
		return "", err
	}
	return body, nil
}

// readBracedBlock takes a string starting with '{' and returns the contents
// between the outermost matching braces. Skips braces inside line comments,
// block comments, and double-quoted/single-quoted/backtick strings.
func readBracedBlock(s string) (string, error) {
	if len(s) == 0 || s[0] != '{' {
		return "", fmt.Errorf("readBracedBlock: input does not start with '{'")
	}
	depth := 0
	i := 0
	const (
		stateCode = iota
		stateLineComment
		stateBlockComment
		stateString
	)
	state := stateCode
	var stringQuote byte
	for ; i < len(s); i++ {
		c := s[i]
		switch state {
		case stateCode:
			switch {
			case c == '/' && i+1 < len(s) && s[i+1] == '/':
				state = stateLineComment
				i++
			case c == '/' && i+1 < len(s) && s[i+1] == '*':
				state = stateBlockComment
				i++
			case c == '"' || c == '\'' || c == '`':
				state = stateString
				stringQuote = c
			case c == '{':
				depth++
			case c == '}':
				depth--
				if depth == 0 {
					return s[1:i], nil
				}
			}
		case stateLineComment:
			if c == '\n' {
				state = stateCode
			}
		case stateBlockComment:
			if c == '*' && i+1 < len(s) && s[i+1] == '/' {
				state = stateCode
				i++
			}
		case stateString:
			if c == '\\' {
				i++ // skip escaped char
				continue
			}
			if c == stringQuote {
				state = stateCode
			}
		}
	}
	return "", fmt.Errorf("unterminated brace block")
}

// onEventRE matches `on(event: "xxx_yyy", ...)` overloads, including the
// multi-line form that splitTopLevelLines collapses with spaces.
var onEventRE = regexp.MustCompile(`(?m)^\s*on\(\s*event:\s*"([a-z_][a-z0-9_]*)"`)

// methodSigRE matches a top-level method declaration, capturing the name.
// Excludes `on(` overloads: those are events.
// Matches:    `methodName<T extends ...>(...)`
//
//	`methodName(...): ReturnType;`
//	`methodName?(...)`
var methodSigRE = regexp.MustCompile(`(?m)^\s*([a-zA-Z_][a-zA-Z0-9_]*)\??\s*[(<]`)

// propertyRE matches a top-level property declaration like `events: EventBus;`.
var propertyRE = regexp.MustCompile(`(?m)^\s*([a-zA-Z_][a-zA-Z0-9_]*)\??\s*:\s*[^;{]+;\s*$`)

func parseExtensionAPI(block string) ExtensionAPISurface {
	var out ExtensionAPISurface
	seenMethod := map[string]bool{}
	seenEvent := map[string]bool{}

	// Walk by top-level lines. Skip nested braces (overload bodies, JSDoc,
	// inline-typed parameters), comment blocks. extractInterfaceBlock has
	// already given us the unwrapped body, so depth resets to 0 here.
	lines := splitTopLevelLines(block)
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "*") || strings.HasPrefix(trim, "/*") {
			continue
		}
		if m := onEventRE.FindStringSubmatch(line); m != nil {
			ev := m[1]
			if !seenEvent[ev] {
				seenEvent[ev] = true
				out.Events = append(out.Events, ev)
			}
			continue
		}
		if m := methodSigRE.FindStringSubmatch(line); m != nil {
			name := m[1]
			if name == "on" {
				continue
			}
			if isReservedTSKeyword(name) {
				continue
			}
			if !seenMethod[name] {
				seenMethod[name] = true
				out.Methods = append(out.Methods, name)
			}
			continue
		}
		if m := propertyRE.FindStringSubmatch(line); m != nil {
			name := m[1]
			if isReservedTSKeyword(name) {
				continue
			}
			if !seenMethod[name] {
				seenMethod[name] = true
				out.Methods = append(out.Methods, name)
			}
		}
	}
	return out
}

// splitTopLevelLines splits a brace-balanced block into "logical lines" where
// each line is one statement at depth 0. Multi-line method declarations
// (e.g. `sendMessage<T = unknown>(\n\tmessage: …,\n\toptions?: …,\n): void;`)
// collapse to a single line so the regexes above can match them.
func splitTopLevelLines(block string) []string {
	var lines []string
	var current strings.Builder
	depth := 0
	const (
		stateCode = iota
		stateLineComment
		stateBlockComment
		stateString
	)
	state := stateCode
	var stringQuote byte
	flush := func() {
		s := current.String()
		current.Reset()
		if strings.TrimSpace(s) == "" {
			return
		}
		lines = append(lines, s)
	}
	for i := 0; i < len(block); i++ {
		c := block[i]
		switch state {
		case stateCode:
			switch {
			case c == '/' && i+1 < len(block) && block[i+1] == '/':
				state = stateLineComment
				i++ // skip the second '/'; comment chars not written to current
			case c == '/' && i+1 < len(block) && block[i+1] == '*':
				state = stateBlockComment
				i++ // skip the '*'; comment chars not written to current
			case c == '"' || c == '\'' || c == '`':
				state = stateString
				stringQuote = c
				current.WriteByte(c)
			case c == '{' || c == '(':
				depth++
				current.WriteByte(c)
			case c == '}' || c == ')':
				depth--
				current.WriteByte(c)
			case c == ';' && depth == 0:
				current.WriteByte(c)
				flush()
			case c == '\n' && depth == 0:
				current.WriteByte(' ') // continuation, fold into one line
			default:
				current.WriteByte(c)
			}
		case stateLineComment:
			// Skip every char until the newline; do NOT write to current.
			if c == '\n' {
				state = stateCode
				// Don't flush: a `// comment` at end of a code line shouldn't
				// terminate the statement. The `;` does that.
			}
		case stateBlockComment:
			// Skip every char until `*/`; do NOT write to current.
			if c == '*' && i+1 < len(block) && block[i+1] == '/' {
				state = stateCode
				i++ // consume the '/'
			}
		case stateString:
			current.WriteByte(c)
			if c == '\\' && i+1 < len(block) {
				current.WriteByte(block[i+1])
				i++
				continue
			}
			if c == stringQuote {
				state = stateCode
			}
		}
	}
	flush()
	return lines
}

// isReservedTSKeyword filters out TS keywords that look like method names but
// aren't (e.g. `readonly`).
func isReservedTSKeyword(name string) bool {
	return slices.Contains([]string{
		"readonly", "private", "public", "protected", "static",
	}, name)
}

// ─── Event interface extraction ──────────────────────────────────────────────

var eventInterfaceRE = regexp.MustCompile(
	`(?m)^export interface ([A-Za-z][A-Za-z0-9]*Event(?:Result)?)\b[^{]*\{`,
)

// extractEventInterfaces finds every `export interface XxxEvent {…}` and
// `export interface XxxEventResult {…}`. Returns name + parsed fields.
func extractEventInterfaces(src string) []EventTypeDecl {
	var out []EventTypeDecl
	for _, loc := range eventInterfaceRE.FindAllStringSubmatchIndex(src, -1) {
		name := src[loc[2]:loc[3]]
		// Skip generic event base aliases that aren't proper events:
		// ToolCallEventBase is a discriminator, not an event surface.
		if strings.HasSuffix(name, "EventBase") {
			continue
		}
		body, err := readBracedBlock(src[loc[1]-1:])
		if err != nil {
			continue
		}
		decl := EventTypeDecl{Name: name, Fields: map[string]string{}}
		for _, f := range parseInterfaceFields(body) {
			if _, ok := decl.Fields[f.name]; ok {
				continue
			}
			decl.Fields[f.name] = f.tsType
			decl.FieldOrder = append(decl.FieldOrder, f.name)
		}
		out = append(out, decl)
	}
	return out
}

type fieldDecl struct {
	name   string
	tsType string
}

// fieldRE matches a top-level field declaration:
//
//	name: type;
//	name?: type;
//	readonly name: type;
//
// Type may contain unions, generics, etc., up to the terminating `;`. The
// `readonly` modifier only restricts assignment in TypeScript; the field is
// still part of the event shape (BeforeAgentStartEvent.systemPrompt).
var fieldRE = regexp.MustCompile(`(?m)^\s*(?:readonly\s+)?([a-zA-Z_][a-zA-Z0-9_]*)\??\s*:\s*([^;]+?);\s*$`)

func parseInterfaceFields(body string) []fieldDecl {
	var out []fieldDecl
	for _, line := range splitTopLevelLines(body) {
		// Skip method signatures (have `(` before the `:`).
		if idx := strings.Index(line, ":"); idx > 0 {
			pre := line[:idx]
			if strings.ContainsAny(pre, "(<") {
				continue
			}
		}
		m := fieldRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		out = append(out, fieldDecl{
			name:   m[1],
			tsType: strings.TrimSpace(m[2]),
		})
	}
	return out
}

// ─── Helpers used by tests ───────────────────────────────────────────────────

// CamelToPascal converts upstream camelCase to Go PascalCase.
// "registerTool" -> "RegisterTool", "exec" -> "Exec".
//
// This helper is for METHOD names where Go style is plain PascalCase. For
// FIELD names that need idiomatic Go initialism handling (Id→ID, Url→URL,
// Api→API, Json→JSON), use [CamelToGoField] instead.
func CamelToPascal(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
}

// goInitialisms is the closed set of word-tokens that idiomatic Go
// upper-cases when they appear at a word boundary inside an identifier.
// Mirrors the small subset of upstream pi's field vocabulary that
// actually needs this treatment; expand only as upstream introduces new
// initialisms (see DIVERGENCES.md U1 sync ritual).
var goInitialisms = map[string]bool{
	"Id":   true,
	"Url":  true,
	"Api":  true,
	"Json": true,
}

// CamelToGoField converts upstream camelCase to Go PascalCase with
// idiomatic Go initialism handling at word boundaries.
//
// Examples:
//
//	"toolCallId"        → "ToolCallID"
//	"newLeafId"         → "NewLeafID"
//	"baseUrl"           → "BaseURL"
//	"apiKey"            → "APIKey"   (leading initialism)
//	"jsonValue"         → "JSONValue"
//	"id"                → "ID"
//	"customInstructions" → "CustomInstructions" (no change)
//
// Word boundaries are detected as transitions from lower-case to upper-case
// after [CamelToPascal] is applied to the input. Each segment is tested
// against [goInitialisms]; matches are upper-cased.
func CamelToGoField(s string) string {
	pascal := CamelToPascal(s)
	if pascal == "" {
		return pascal
	}
	// Split on lower→upper boundaries: walk runes, start a new segment
	// whenever we see Upper preceded by Lower, OR at index 0.
	runes := []rune(pascal)
	type seg struct{ start, end int }
	var segs []seg
	segStart := 0
	for i := 1; i < len(runes); i++ {
		if isUpper(runes[i]) && isLower(runes[i-1]) {
			segs = append(segs, seg{segStart, i})
			segStart = i
		}
	}
	segs = append(segs, seg{segStart, len(runes)})

	var b strings.Builder
	for _, sg := range segs {
		word := string(runes[sg.start:sg.end])
		if goInitialisms[word] {
			b.WriteString(strings.ToUpper(word))
		} else {
			b.WriteString(word)
		}
	}
	return b.String()
}

func isUpper(r rune) bool { return r >= 'A' && r <= 'Z' }
func isLower(r rune) bool { return r >= 'a' && r <= 'z' }

// SnakeEventToOnMethod converts an upstream event name to its expected Go
// `On*` method name. "tool_call" -> "OnToolCall", "session_before_compact"
// -> "OnSessionBeforeCompact".
func SnakeEventToOnMethod(event string) string {
	parts := strings.Split(event, "_")
	var b strings.Builder
	b.WriteString("On")
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(CamelToPascal(p))
	}
	return b.String()
}
