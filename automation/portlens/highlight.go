// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"html"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"unsafe"

	"github.com/alecthomas/chroma/v2"
	chtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/dlclark/regexp2"
)

var (
	emptyStyle  = chroma.MustNewStyle("portlens", chroma.StyleEntries{})
	hlFormatter = chtml.New(chtml.WithClasses(true), chtml.PreventSurroundingPre(true))
	// punctuationRe drops the classes that carry no color, so a highlighted snippet stays small.
	punctuationRe = regexp.MustCompile(`<span class="(?:p|nx|w|nb|bp|pi|nn)">([^<]*)</span>`)

	untimedMu sync.Mutex
	untimed   = map[string]chroma.Lexer{}
)

// lexerFor is chroma's lexer for lang with no match deadline. chroma compiles every rule with a 250 ms regexp2 MatchTimeout
// (regexp.go maybeCompile) and reads a timed-out match as no match, so a match descheduled for 250 ms on a loaded host
// tokenises the same text differently. Without the deadline the tokens depend only on the text. The Go lexer tokenises raw
// strings with the Go text template lexer, which gets the same treatment.
func lexerFor(lang string) chroma.Lexer {
	untimedMu.Lock()
	defer untimedMu.Unlock()
	if lexer, ok := untimed[lang]; ok {
		return lexer
	}
	lexer := lexers.Get(lang)
	if lexer != nil {
		removeMatchDeadline(lexer)
		if lang == "go" {
			removeMatchDeadline(lexers.GoTextTemplate)
		}
	}
	untimed[lang] = lexer
	return lexer
}

func removeMatchDeadline(lexer chroma.Lexer) {
	for _, rules := range compiledRules(lexer) {
		for _, rule := range rules {
			rule.Regexp.MatchTimeout = regexp2.DefaultMatchTimeout
		}
	}
}

// compiledRules compiles a regex lexer's rules and returns them; chroma keeps the compiled table unexported. Other lexers have none.
func compiledRules(lexer chroma.Lexer) chroma.CompiledRules {
	regexLexer, ok := lexer.(*chroma.RegexLexer)
	if !ok {
		return nil
	}
	if _, err := regexLexer.Tokenise(nil, ""); err != nil {
		return nil
	}
	field := reflect.ValueOf(regexLexer).Elem().FieldByName("rules")
	if !field.IsValid() || field.Type() != reflect.TypeFor[map[string][]*chroma.CompiledRule]() {
		panic(fmt.Sprintf("chroma.RegexLexer has no rules table of type map[string][]*chroma.CompiledRule (%v): highlight cannot remove the match deadline that makes its output depend on the clock", field))
	}
	return *(*chroma.CompiledRules)(unsafe.Pointer(field.UnsafeAddr())) //nolint:gosec // G103: reads chroma's unexported compiled-rule table, whose type is checked above; lexerFor sets each rule's MatchTimeout before any caller tokenises with the lexer.
}

// languageFor maps a file name to a chroma lexer name.
func languageFor(path string) string {
	switch {
	case strings.HasSuffix(path, ".go"):
		return "go"
	case strings.HasSuffix(path, ".ts"), strings.HasSuffix(path, ".tsx"), strings.HasSuffix(path, ".mts"):
		return "typescript"
	case strings.HasSuffix(path, ".js"), strings.HasSuffix(path, ".mjs"), strings.HasSuffix(path, ".cjs"):
		return "javascript"
	case strings.HasSuffix(path, ".rs"):
		return "rust"
	case strings.HasSuffix(path, ".py"):
		return "python"
	case strings.HasSuffix(path, ".toml"):
		return "toml"
	case strings.HasSuffix(path, ".json"):
		return "json"
	}
	return ""
}

// highlight renders code as HTML spans with chroma token classes. Colors come from the stylesheet, so the output follows the light and dark themes. Unknown languages and tokenizer failures return the escaped text.
func highlight(lang, code string) string {
	if lang == "" {
		return html.EscapeString(code)
	}
	lexer := lexerFor(lang)
	if lexer == nil {
		return html.EscapeString(code)
	}
	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return html.EscapeString(code)
	}
	var sb strings.Builder
	if err := hlFormatter.Format(&sb, emptyStyle, it); err != nil {
		return html.EscapeString(code)
	}
	out := punctuationRe.ReplaceAllString(sb.String(), "$1")
	// The lexer ends the text with a newline, often inside the last token's span; dropping it keeps the snippet at the cited lines.
	out = strings.TrimSuffix(out, "\n")
	if before, ok := strings.CutSuffix(out, "\n</span>"); ok {
		out = before + "</span>"
	}
	return out
}
