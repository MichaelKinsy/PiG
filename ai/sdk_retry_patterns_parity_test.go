package ai

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestSDKRetryAndNonOverflowPatternsMatchAI extracts the retryable provider error,
// non-retryable provider limit, and non-overflow pattern lists from all four extension
// SDK source files (Go, Python, Rust, Common Lisp) and compares them element-for-element
// against ai/assistant_retry.go and ai/overflow.go so any missing or misplaced entry fails
// immediately.
func TestSDKRetryAndNonOverflowPatternsMatchAI(t *testing.T) {
	aiRetry := extractGoVarPatterns(t, "assistant_retry.go", "retryableProviderErrorPattern")
	aiNonRetry := extractGoVarPatterns(t, "assistant_retry.go", "nonRetryableProviderLimitErrorPattern")
	aiNonOverflow := extractGoNonOverflowPatterns(t, "overflow.go")

	if len(aiRetry) == 0 || len(aiNonRetry) == 0 || len(aiNonOverflow) == 0 {
		t.Fatalf("empty ai reference lists: retry=%d nonRetry=%d nonOverflow=%d", len(aiRetry), len(aiNonRetry), len(aiNonOverflow))
	}

	sdks := []struct {
		name        string
		retry       []string
		nonRetry    []string
		nonOverflow []string
	}{
		{
			name:        "extensions/sdk",
			retry:       extractGoVarPatterns(t, filepath.Join("..", "extensions", "sdk", "pi_ai_models.go"), "retryableProviderErrorPattern"),
			nonRetry:    extractGoVarPatterns(t, filepath.Join("..", "extensions", "sdk", "pi_ai_models.go"), "nonRetryableProviderLimitErrorPattern"),
			nonOverflow: extractGoVarPatterns(t, filepath.Join("..", "extensions", "sdk", "pi_ai_models.go"), "nonOverflowPatterns"),
		},
		{
			name:        "extensions/sdk-py",
			retry:       extractPyListPatterns(t, filepath.Join("..", "extensions", "sdk-py", "pig_sdk", "pi_ai_helpers.py"), "_RETRYABLE_PROVIDER_ERROR_PATTERN"),
			nonRetry:    extractPyListPatterns(t, filepath.Join("..", "extensions", "sdk-py", "pig_sdk", "pi_ai_helpers.py"), "_NON_RETRYABLE_PROVIDER_LIMIT_ERROR_PATTERN"),
			nonOverflow: extractPyCallPatterns(t, filepath.Join("..", "extensions", "sdk-py", "pig_sdk", "pi_ai_helpers.py"), "_NON_OVERFLOW_PATTERNS"),
		},
		{
			name:        "extensions/sdk-rs",
			retry:       extractRustSlicePatterns(t, filepath.Join("..", "extensions", "sdk-rs", "src", "pi_ai_helpers.rs"), "RETRYABLE_PROVIDER_ERROR_PATTERN"),
			nonRetry:    extractRustSlicePatterns(t, filepath.Join("..", "extensions", "sdk-rs", "src", "pi_ai_helpers.rs"), "NON_RETRYABLE_PROVIDER_LIMIT_ERROR_PATTERN"),
			nonOverflow: extractRustSlicePatterns(t, filepath.Join("..", "extensions", "sdk-rs", "src", "pi_ai_helpers.rs"), "NON_OVERFLOW_PATTERNS"),
		},
		{
			name:        "extensions/sdk-lisp",
			retry:       extractLispDefvarPatterns(t, filepath.Join("..", "extensions", "sdk-lisp", "src", "pi-ai.lisp"), "*ai-retryable-provider-error*"),
			nonRetry:    extractLispDefvarPatterns(t, filepath.Join("..", "extensions", "sdk-lisp", "src", "pi-ai.lisp"), "*ai-non-retryable-provider-limit-error*"),
			nonOverflow: extractLispDefvarPatterns(t, filepath.Join("..", "extensions", "sdk-lisp", "src", "pi-ai.lisp"), "*ai-non-overflow-patterns*"),
		},
	}

	wantRetry := canonicalizePatterns(aiRetry)
	wantNonRetry := canonicalizePatterns(aiNonRetry)
	wantNonOverflow := canonicalizePatterns(aiNonOverflow)

	for _, sdk := range sdks {
		t.Run(sdk.name, func(t *testing.T) {
			assertPatternsEqual(t, "retryable", canonicalizePatterns(sdk.retry), wantRetry)
			assertPatternsEqual(t, "nonRetryable", canonicalizePatterns(sdk.nonRetry), wantNonRetry)
			assertPatternsEqual(t, "nonOverflow", canonicalizePatterns(sdk.nonOverflow), wantNonOverflow)
		})
	}
}

func assertPatternsEqual(t *testing.T, label string, got, want []string) {
	t.Helper()
	if slices.Equal(got, want) {
		return
	}
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("%s missing pattern: %q", label, w)
		}
	}
	for _, g := range got {
		if !slices.Contains(want, g) {
			t.Errorf("%s unexpected extra pattern: %q", label, g)
		}
	}
	if len(got) == len(want) {
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s order mismatch at index %d: got %q, want %q", label, i, got[i], want[i])
				break
			}
		}
	}
}

// canonicalizePatterns normalizes RE2 lookaround workarounds and explicit JS whitespace class expansions
// so Go RE2 patterns compare identically against PCRE/Python/Rust/Lisp regex spellings.
func canonicalizePatterns(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		s = strings.ReplaceAll(s, jsSpaceClass, `\s`)
		s = strings.ReplaceAll(s, `(?:^|[^a-z0-9])`, `(?<![a-z0-9])`)
		s = strings.ReplaceAll(s, `(?:[^a-z0-9]|$)`, `(?![a-z0-9])`)
		s = strings.ReplaceAll(s, `(?:^|[^a-z0-9_])`, `(?<![a-z0-9_])`)
		s = strings.ReplaceAll(s, `(?:[^a-z0-9_]|$)`, `(?![a-z0-9_])`)
		s = strings.ReplaceAll(s, `([^0-9]|$)`, `(?![0-9])`)
		out[i] = s
	}
	return out
}

func evalGoStringExpr(t *testing.T, expr ast.Expr) string {
	t.Helper()
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			t.Fatalf("unexpected basic literal kind %v", e.Kind)
		}
		v, err := strconv.Unquote(e.Value)
		if err != nil {
			t.Fatalf("unquote %s: %v", e.Value, err)
		}
		return v
	case *ast.Ident:
		if e.Name == "jsSpaceClass" {
			return jsSpaceClass
		}
		t.Fatalf("unexpected ident %q in pattern expression", e.Name)
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			return evalGoStringExpr(t, e.X) + evalGoStringExpr(t, e.Y)
		}
		t.Fatalf("unexpected binary op %v", e.Op)
	default:
		t.Fatalf("unexpected AST expr %T", expr)
	}
	return ""
}

func extractGoVarPatterns(t *testing.T, path, varName string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if name.Name != varName {
					continue
				}
				call, ok := vs.Values[i].(*ast.CallExpr)
				if !ok {
					t.Fatalf("%s: %s value is %T, want *ast.CallExpr", path, varName, vs.Values[i])
				}
				var exprs []ast.Expr
				if len(call.Args) == 1 {
					if comp, ok := call.Args[0].(*ast.CompositeLit); ok {
						exprs = comp.Elts
					} else {
						exprs = call.Args
					}
				} else {
					exprs = call.Args
				}
				out := make([]string, 0, len(exprs))
				for _, e := range exprs {
					out = append(out, evalGoStringExpr(t, e))
				}
				return out
			}
		}
	}
	t.Fatalf("%s: var %s not found", path, varName)
	return nil
}

func extractGoNonOverflowPatterns(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if name.Name != "nonOverflowPatterns" {
					continue
				}
				comp, ok := vs.Values[i].(*ast.CompositeLit)
				if !ok {
					t.Fatalf("%s: nonOverflowPatterns is %T", path, vs.Values[i])
				}
				out := make([]string, 0, len(comp.Elts))
				for _, elt := range comp.Elts {
					call, ok := elt.(*ast.CallExpr)
					if !ok || len(call.Args) != 1 {
						t.Fatalf("%s: unexpected element %T in nonOverflowPatterns", path, elt)
					}
					out = append(out, evalGoStringExpr(t, call.Args[0]))
				}
				return out
			}
		}
	}
	t.Fatalf("%s: var nonOverflowPatterns not found", path)
	return nil
}

var pyStringTokenRe = regexp.MustCompile(`(?m)#[^\n]*|r'(?:[^'\\]|\\.)*'|r"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"`)

func extractPyListPatterns(t *testing.T, path, name string) []string {
	t.Helper()
	src := mustReadString(t, path)
	idx := strings.Index(src, name+" = _js_regexp(\"|\".join([")
	if idx < 0 {
		t.Fatalf("%s: %s not found", path, name)
	}
	rest := src[idx:]
	start := strings.Index(rest, "[")
	end := strings.Index(rest, "]))")
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("%s: %s list bounds not found", path, name)
	}
	return parsePyStrings(t, rest[start+1:end])
}

func extractPyCallPatterns(t *testing.T, path, name string) []string {
	t.Helper()
	src := mustReadString(t, path)
	idx := strings.Index(src, name+" = _compile_each(")
	if idx < 0 {
		t.Fatalf("%s: %s not found", path, name)
	}
	rest := src[idx:]
	start := strings.Index(rest, "(")
	end := strings.Index(rest, "\n")
	if start < 0 || end < 0 {
		t.Fatalf("%s: %s bounds not found", path, name)
	}
	return parsePyStrings(t, rest[start+1:end])
}

func parsePyStrings(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	for _, tok := range pyStringTokenRe.FindAllString(body, -1) {
		if strings.HasPrefix(tok, "#") {
			continue
		}
		if strings.HasPrefix(tok, `r"`) || strings.HasPrefix(tok, `r'`) {
			out = append(out, tok[2:len(tok)-1])
			continue
		}
		if strings.HasPrefix(tok, `'`) {
			v, err := strconv.Unquote(`"` + strings.ReplaceAll(tok[1:len(tok)-1], `"`, `\"`) + `"`)
			if err != nil {
				t.Fatalf("unquote python single-quoted string %s: %v", tok, err)
			}
			out = append(out, v)
			continue
		}
		v, err := strconv.Unquote(tok)
		if err != nil {
			t.Fatalf("unquote python string %s: %v", tok, err)
		}
		out = append(out, v)
	}
	return out
}

func extractRustSlicePatterns(t *testing.T, path, name string) []string {
	t.Helper()
	src := mustReadString(t, path)
	idx := strings.Index(src, "static "+name+":")
	if idx < 0 {
		t.Fatalf("%s: %s not found", path, name)
	}
	rest := src[idx:]
	start := strings.Index(rest, "&[")
	end := strings.Index(rest, "\n    ])")
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("%s: %s slice bounds not found", path, name)
	}
	body := rest[start+2 : end]
	var out []string
	for i := 0; i < len(body); {
		switch {
		case strings.HasPrefix(body[i:], "//"):
			if nl := strings.IndexByte(body[i:], '\n'); nl >= 0 {
				i += nl + 1
			} else {
				i = len(body)
			}
		case strings.HasPrefix(body[i:], `r#"`):
			j := strings.Index(body[i+3:], `"#`)
			if j < 0 {
				t.Fatalf("%s: unterminated r# literal in %s", path, name)
			}
			out = append(out, body[i+3:i+3+j])
			i += 3 + j + 2
		case strings.HasPrefix(body[i:], `r"`):
			j := strings.IndexByte(body[i+2:], '"')
			if j < 0 {
				t.Fatalf("%s: unterminated raw literal in %s", path, name)
			}
			out = append(out, body[i+2:i+2+j])
			i += 2 + j + 1
		case body[i] == '"':
			j := i + 1
			for j < len(body) {
				if body[j] == '\\' {
					j += 2
					continue
				}
				if body[j] == '"' {
					break
				}
				j++
			}
			if j >= len(body) {
				t.Fatalf("%s: unterminated string literal in %s", path, name)
			}
			v, err := strconv.Unquote(body[i : j+1])
			if err != nil {
				t.Fatalf("unquote rust string %s: %v", body[i:j+1], err)
			}
			out = append(out, v)
			i = j + 1
		default:
			i++
		}
	}
	return out
}

var lispStringTokenRe = regexp.MustCompile(`(?m);[^\n]*|"(?:[^"\\]|\\.)*"`)

func extractLispDefvarPatterns(t *testing.T, path, name string) []string {
	t.Helper()
	src := mustReadString(t, path)
	idx := strings.Index(src, "(defvar "+name)
	if idx < 0 {
		t.Fatalf("%s: %s not found", path, name)
	}
	// Walk balanced parentheses from idx to find the end of this defvar form.
	depth := 0
	inStr := false
	escaped := false
	inComment := false
	end := -1
	for i := idx; i < len(src); i++ {
		c := src[i]
		if inComment {
			if c == '\n' {
				inComment = false
			}
			continue
		}
		if inStr {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case ';':
			inComment = true
		case '"':
			inStr = true
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = i + 1
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		t.Fatalf("%s: unterminated defvar %s", path, name)
	}
	form := src[idx:end]
	var out []string
	for _, tok := range lispStringTokenRe.FindAllString(form, -1) {
		if strings.HasPrefix(tok, ";") {
			continue
		}
		v, err := strconv.Unquote(tok)
		if err != nil {
			t.Fatalf("unquote lisp string %s: %v", tok, err)
		}
		out = append(out, v)
	}
	return out
}

func mustReadString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
