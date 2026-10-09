// SPDX-License-Identifier: MIT

package upgrade

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
)

// Diagnostic is one positioned compiler message.
type Diagnostic struct {
	File    string
	Line    int
	Col     int
	Message string
}

// ParseDiagnostics reads the positioned messages of `go build` or `go vet`
// output: `path/file.go:12:9: message`. Package headers, `#` lines and
// continuation lines are not diagnostics.
func ParseDiagnostics(output []byte) []Diagnostic {
	var diagnostics []Diagnostic
	for raw := range bytes.SplitSeq(output, []byte{'\n'}) {
		line := strings.TrimRight(string(raw), "\r")
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "\t") || strings.HasPrefix(line, " ") {
			continue
		}
		marker := strings.Index(line, ".go:")
		if marker < 0 {
			continue
		}
		file := line[:marker+len(".go")]
		rest := line[marker+len(".go:"):]
		number, rest, ok := cutNumber(rest)
		if !ok {
			continue
		}
		diagnostic := Diagnostic{File: file, Line: number}
		if column, afterColumn, found := cutNumber(rest); found {
			diagnostic.Col, rest = column, afterColumn
		}
		diagnostic.Message = strings.TrimSpace(rest)
		if diagnostic.Message == "" {
			continue
		}
		diagnostics = append(diagnostics, diagnostic)
	}
	return diagnostics
}

// cutNumber reads the decimal number at the start of text and the colon after
// it.
func cutNumber(text string) (number int, rest string, ok bool) {
	digits := len(text) - len(strings.TrimLeft(text, "0123456789"))
	if digits == 0 || digits >= len(text) || text[digits] != ':' {
		return 0, text, false
	}
	value, err := strconv.Atoi(text[:digits])
	if err != nil {
		return 0, text, false
	}
	return value, text[digits+1:], true
}

// Drift is one SDK change an extension was written for the old shape of.
type Drift struct {
	// Rule is the rule that covers the change.
	Rule string `json:"rule"`
	// Symbol is the SDK symbol, Old and New its shapes, and Since the SDK
	// release that changed it.
	Symbol string `json:"symbol"`
	Old    string `json:"old"`
	New    string `json:"new"`
	Since  string `json:"since"`
	// Rewrites reports whether pig upgrades this use mechanically.
	Rewrites bool `json:"rewrites"`
	// File and Line are the first use. Count is the number of uses seen.
	File  string `json:"file"`
	Line  int    `json:"line"`
	Count int    `json:"count"`
}

// String names the change with its old and new shape.
func (d Drift) String() string {
	return d.Symbol + ": " + d.Old + " -> " + d.New + " (SDK " + d.Since + ")"
}

// FileSource reads a source file named by a diagnostic.
type FileSource func(path string) ([]byte, error)

// Classify maps compiler diagnostics that name an SDK symbol to the SDK change
// behind them. A diagnostic no rule recognizes is not drift: the caller keeps
// the compiler's own message for it. Uses of one symbol are one Drift, in the
// order of the first use.
//
// A diagnostic that does not name the symbol, such as a bool where the SDK
// wants a *bool, is read from the source file the diagnostic points at, which
// source supplies. Without source those diagnostics are not classified.
func Classify(diagnostics []Diagnostic, source FileSource) []Drift {
	var drifts []Drift
	index := map[string]int{}
	files := map[string]*parsedFile{}
	for _, diagnostic := range diagnostics {
		ruleID, symbol, ok := classifyDiagnostic(diagnostic, source, files)
		if !ok {
			continue
		}
		if at, seen := index[symbol]; seen {
			drifts[at].Count++
			continue
		}
		rule, _ := RuleByID(ruleID)
		change, _ := changeFor(symbol)
		index[symbol] = len(drifts)
		drifts = append(drifts, Drift{
			Rule: ruleID, Symbol: symbol, Old: change.Old, New: change.New, Since: change.Since,
			Rewrites: rule.Rewrites, File: diagnostic.File, Line: diagnostic.Line, Count: 1,
		})
	}
	return drifts
}

var (
	usageDiagnostic     = regexp.MustCompile(`\.(Tokens|Percent)\b`)
	usagePointerTypes   = regexp.MustCompile(`\*int\b|\*float64\b`)
	boolPointerArgument = regexp.MustCompile(`^cannot use .+ as \*bool value in `)
)

// isArityDiagnostic reports whether message is one the compiler gives for a
// call that returns two results where one is wanted.
func isArityDiagnostic(message string) bool {
	for _, prefix := range []string{"assignment mismatch:", "multiple-value ", "too many arguments in call", "too many return values"} {
		if strings.HasPrefix(message, prefix) {
			return true
		}
	}
	return false
}

func classifyDiagnostic(diagnostic Diagnostic, source FileSource, files map[string]*parsedFile) (rule, symbol string, ok bool) {
	message := diagnostic.Message
	if name, found := getterInDiagnostic(message); found {
		if _, isGetter := getterByName(name); isGetter && importsSDK(diagnostic, source, files) {
			return RuleContextGetters, "Context." + name, true
		}
		return "", "", false
	}
	if isArityDiagnostic(message) {
		if name, found := getterCalledAt(diagnostic, source, files); found {
			return RuleContextGetters, "Context." + name, true
		}
		return "", "", false
	}
	switch {
	case boolPointerArgument.MatchString(message):
		if field, found := sdkBoolField(diagnostic, source, files); found {
			return RuleOptionalBool, field, true
		}
	case usageDiagnostic.MatchString(message) && usagePointerTypes.MatchString(message):
		if match := usageDiagnostic.FindStringSubmatch(message); importsSDK(diagnostic, source, files) {
			return RuleContextUsage, "ContextUsage." + match[1], true
		}
	case strings.Contains(message, "SetEditorComponent") && strings.Contains(message, "EditorFactory"):
		return RuleEditorComponent, "Context.SetEditorComponent", true
	case strings.Contains(message, "ToolWithConstrainedSampling") && strings.Contains(message, "ToolConstrainedSampling"):
		return RuleConstrainedSampling, "(*Extension).ToolWithConstrainedSampling", true
	case strings.Contains(message, "AddAutocompleteProvider") || strings.Contains(message, "AutocompleteProviderFactory"):
		return RuleAutocompleteFactory, "AutocompleteProviderFactory", true
	case (strings.Contains(message, "cannot be compared") || strings.Contains(message, "invalid map key")) && strings.Contains(message, "ToolInfo"):
		return RuleNotComparable, "ToolInfo", true
	case (strings.Contains(message, "cannot be compared") || strings.Contains(message, "invalid map key")) && strings.Contains(message, "OAuthCredentials"):
		return RuleNotComparable, "OAuthCredentials", true
	}
	return "", "", false
}

// getterInDiagnostic returns the method a single-value-context diagnostic
// names: `assignment mismatch: 1 variable but ctx.GetSessionID returns 2
// values` and `multiple-value ctx.IsIdle() (value of type (bool, error)) in
// single-value context`.
func getterInDiagnostic(message string) (string, bool) {
	var expression string
	switch {
	case strings.HasPrefix(message, "assignment mismatch:"):
		_, after, found := strings.Cut(message, " but ")
		if !found {
			return "", false
		}
		before, _, found := strings.Cut(after, " returns ")
		if !found {
			return "", false
		}
		expression = before
	case strings.HasPrefix(message, "multiple-value "):
		after := strings.TrimPrefix(message, "multiple-value ")
		before, _, found := strings.Cut(after, " (value of type ")
		if !found {
			return "", false
		}
		expression = before
	default:
		return "", false
	}
	return calleeName(expression)
}

// calleeName returns the method name at the end of a call expression's text,
// with or without its argument list.
func calleeName(expression string) (string, bool) {
	expression = strings.TrimSpace(expression)
	if strings.HasSuffix(expression, ")") {
		depth := 0
		open := -1
		for i := len(expression) - 1; i >= 0; i-- {
			switch expression[i] {
			case ')':
				depth++
			case '(':
				depth--
			}
			if depth == 0 {
				open = i
				break
			}
		}
		if open < 0 {
			return "", false
		}
		expression = expression[:open]
	}
	dot := strings.LastIndex(expression, ".")
	name := expression[dot+1:]
	if name == "" || strings.ContainsAny(name, " ()[]{}\"'*&") {
		return "", false
	}
	return name, true
}

// parsedFile is a source file read for a diagnostic.
type parsedFile struct {
	fset   *token.FileSet
	syntax *ast.File
	tokens *token.File
	// sdkNames are the names the file gives the SDK import.
	sdkNames map[string]bool
}

func loadFile(diagnostic Diagnostic, source FileSource, files map[string]*parsedFile) *parsedFile {
	if source == nil {
		return nil
	}
	if cached, seen := files[diagnostic.File]; seen {
		return cached
	}
	var parsed *parsedFile
	if data, err := source(diagnostic.File); err == nil {
		fset := token.NewFileSet()
		if syntax, err := parser.ParseFile(fset, diagnostic.File, data, parser.SkipObjectResolution); err == nil {
			parsed = &parsedFile{fset: fset, syntax: syntax, tokens: fset.File(syntax.Pos()), sdkNames: sdkImportNames(syntax)}
		}
	}
	files[diagnostic.File] = parsed
	return parsed
}

// sdkImportNames returns the names under which a file imports the SDK.
func sdkImportNames(file *ast.File) map[string]bool {
	names := map[string]bool{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || !IsSDKPath(path) {
			continue
		}
		switch {
		case spec.Name == nil:
			names["sdk"] = true
		case spec.Name.Name != "_" && spec.Name.Name != ".":
			names[spec.Name.Name] = true
		}
	}
	return names
}

// importsSDK reports whether the file a diagnostic points at imports the SDK.
// Without source it reports true: the diagnostic named the SDK symbol itself.
func importsSDK(diagnostic Diagnostic, source FileSource, files map[string]*parsedFile) bool {
	if source == nil {
		return true
	}
	parsed := loadFile(diagnostic, source, files)
	if parsed == nil {
		return false
	}
	for _, spec := range parsed.syntax.Imports {
		if path, err := strconv.Unquote(spec.Path.Value); err == nil && IsSDKPath(path) {
			return true
		}
	}
	return false
}

// sdkBoolField returns "Type.Field" when the diagnostic points at the value of
// a struct-literal field of an SDK type: `sdk.SendMessageOptions{TriggerTurn:
// true}`.
func sdkBoolField(diagnostic Diagnostic, source FileSource, files map[string]*parsedFile) (string, bool) {
	parsed := loadFile(diagnostic, source, files)
	if parsed == nil || diagnostic.Line < 1 || diagnostic.Line > parsed.tokens.LineCount() || len(parsed.sdkNames) == 0 {
		return "", false
	}
	position := parsed.tokens.LineStart(diagnostic.Line) + token.Pos(max(diagnostic.Col, 1)-1)
	var field string
	ast.Inspect(parsed.syntax, func(node ast.Node) bool {
		if field != "" || node == nil || position < node.Pos() || position > node.End() {
			return field == "" && node != nil
		}
		literal, isLiteral := node.(*ast.CompositeLit)
		if !isLiteral {
			return true
		}
		selector, isSelector := literal.Type.(*ast.SelectorExpr)
		if !isSelector {
			return true
		}
		qualifier, isIdent := selector.X.(*ast.Ident)
		if !isIdent || !parsed.sdkNames[qualifier.Name] {
			return true
		}
		for _, element := range literal.Elts {
			pair, isPair := element.(*ast.KeyValueExpr)
			if !isPair || pair.Value.Pos() != position {
				continue
			}
			if key, isKey := pair.Key.(*ast.Ident); isKey {
				field = selector.Sel.Name + "." + key.Name
			}
		}
		return true
	})
	if field == "" {
		return "", false
	}
	if _, known := changeFor(field); !known {
		return "", false
	}
	return field, true
}

// getterCalledAt returns the Context getter called at the position of an arity
// diagnostic: `strings.TrimSpace(ctx.GetSystemPrompt())` fails with `too many
// arguments in call` and does not name the getter.
func getterCalledAt(diagnostic Diagnostic, source FileSource, files map[string]*parsedFile) (string, bool) {
	parsed := loadFile(diagnostic, source, files)
	if parsed == nil || len(parsed.sdkNames) == 0 || diagnostic.Line < 1 || diagnostic.Line > parsed.tokens.LineCount() {
		return "", false
	}
	position := parsed.tokens.LineStart(diagnostic.Line) + token.Pos(max(diagnostic.Col, 1)-1)
	var name string
	ast.Inspect(parsed.syntax, func(node ast.Node) bool {
		if name != "" || node == nil || position < node.Pos() || position > node.End() {
			return name == "" && node != nil
		}
		call, isCall := node.(*ast.CallExpr)
		if !isCall || call.Pos() != position {
			return true
		}
		if selector, isSelector := call.Fun.(*ast.SelectorExpr); isSelector {
			if _, isGetter := getterByName(selector.Sel.Name); isGetter {
				name = selector.Sel.Name
			}
		}
		return true
	})
	return name, name != ""
}
