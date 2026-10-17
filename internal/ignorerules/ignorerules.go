// Package ignorerules applies .gitignore, .ignore, and .fdignore files the way
// upstream Pi's package manager does when it discovers package resources.
package ignorerules

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// matchEverything is the empty expression, which matches every path. Rules share it; a *regexp.Regexp is safe for concurrent use.
var matchEverything = lazyregexp.New("")

// Rule is one pattern from a .gitignore, .ignore, or .fdignore file, prefixed with its directory relative to the walk root. Its immutable matcher belongs to that walk; a reload constructs fresh rules.
type Rule struct {
	negated      bool
	dirOnly      bool
	basenameOnly bool
	matcher      *regexp.Regexp
}

// Append adds the rules from dir's .gitignore, .ignore, and .fdignore files,
// as upstream Pi's addIgnoreRules (core/package-manager.ts) does.
func Append(rules []Rule, dir, root string) []Rule {
	relDir, err := filepath.Rel(root, dir)
	if err != nil {
		return rules
	}
	prefix := ""
	if relDir != "." {
		prefix = filepath.ToSlash(relDir) + "/"
	}
	for _, name := range []string{".gitignore", ".ignore", ".fdignore"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			if pattern, ok := prefixIgnorePattern(line, prefix); ok {
				rules = appendRule(rules, pattern)
			}
		}
	}
	return rules
}

// prefixIgnorePattern is package-manager.ts prefixIgnorePattern: it drops a comment or blank line, turns a leading `\!` into `!` (which the matcher then reads as a negation, as in Pi), removes one leading slash, and prefixes the directory.
func prefixIgnorePattern(line, prefix string) (string, bool) {
	trimmed := jsstring.Trim(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, `\#`) {
		return "", false
	}
	pattern, negated := line, false
	if rest, ok := strings.CutPrefix(pattern, "!"); ok {
		negated, pattern = true, rest
	} else if strings.HasPrefix(pattern, `\!`) {
		pattern = pattern[1:]
	}
	pattern = strings.TrimPrefix(pattern, "/")
	pattern = prefix + pattern
	if negated {
		pattern = "!" + pattern
	}
	return pattern, true
}

// AppendPatterns appends already root-relative ignore patterns without filesystem access. It preserves negation and escaping for environment-backed resource loaders.
func AppendPatterns(rules []Rule, patterns []string) []Rule {
	for _, pattern := range patterns {
		rules = appendRule(rules, pattern)
	}
	return rules
}

func appendRule(rules []Rule, pattern string) []Rule {
	if rule, ok := newRule(pattern); ok {
		rules = append(rules, rule)
	}
	return rules
}

// Ignored reports whether candidate, relative to root, is excluded by rules. As in the `ignore` package, a path inside an ignored directory is ignored; a rule can not re-include it.
func Ignored(candidate string, directory bool, root string, rules []Rule) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || len(rules) == 0 {
		return false
	}
	rel = filepath.ToSlash(rel)
	for i := 0; i < len(rel); i++ {
		if rel[i] == '/' && ruleSetIgnores(rel[:i], true, rules) {
			return true
		}
	}
	return ruleSetIgnores(rel, directory, rules)
}

func ruleSetIgnores(rel string, directory bool, rules []Rule) bool {
	ignored := false
	for _, rule := range rules {
		if rule.match(rel, directory) {
			ignored = !rule.negated
		}
	}
	return ignored
}

// newRule reads one gitignore line the way the `ignore` package does: `#` starts a comment, `!` negates, a trailing slash selects directories, and a pattern with no slash before its end matches a name at any depth.
func newRule(pattern string) (Rule, bool) {
	raw := pattern
	pattern = trimTrailingSpaces(pattern)
	if pattern == "" || strings.HasPrefix(pattern, "#") || endsInLoneBackslash(pattern) {
		return Rule{}, false
	}
	rule := Rule{}
	switch {
	case strings.HasPrefix(pattern, "!"):
		rule.negated, pattern, raw = true, pattern[1:], raw[1:]
	case strings.HasPrefix(pattern, `\!`), strings.HasPrefix(pattern, `\#`):
		pattern, raw = pattern[1:], raw[1:]
	}
	if pattern == "" {
		// The `ignore` package compiles "!" to an empty expression, which matches every path.
		if rule.negated {
			rule.matcher = matchEverything.Regexp()
			return rule, true
		}
		return Rule{}, false
	}
	rule.dirOnly = strings.HasSuffix(pattern, "/")
	body := strings.TrimSuffix(pattern, "/")
	body = strings.TrimPrefix(body, "/")
	if body == "" {
		return Rule{}, false
	}
	// `ignore` decides this on the pattern as written, before it trims trailing spaces: "name/ " is anchored.
	rule.basenameOnly = !hasInnerSlash(raw)
	rule.matcher = compileGlob(body, rule.dirOnly)
	return rule, true
}

// hasInnerSlash reports whether the pattern has a slash that is not its last character.
func hasInnerSlash(pattern string) bool {
	slash := strings.IndexByte(pattern, '/')
	return slash >= 0 && slash < len(pattern)-1
}

// trimTrailingSpaces removes the spaces at the end of a pattern unless the last one is escaped.
func trimTrailingSpaces(pattern string) string {
	for strings.HasSuffix(pattern, " ") {
		backslashes := 0
		for i := len(pattern) - 2; i >= 0 && pattern[i] == '\\'; i-- {
			backslashes++
		}
		if backslashes%2 == 1 {
			break
		}
		pattern = pattern[:len(pattern)-1]
	}
	return pattern
}

func endsInLoneBackslash(pattern string) bool {
	backslashes := 0
	for i := len(pattern) - 1; i >= 0 && pattern[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

// globToken is one element of a translated pattern: a literal, a `*`, a `?`, a bracket expression or a group that bounds a run of wildcards.
type globToken struct {
	kind   byte // 'l' literal, '*' star, '?' any character, 'c' class, 'g' group
	source string
	char   string
}

// compileGlob translates a gitignore pattern body to an expression over a slash-separated path, matched case-insensitively as `ignore()` does by default.
// A `*` followed in the same run by another token stops before that token's character, as the `ignore` package pins its wildcards (pinWildcards), except for the last wildcard of a run.
func compileGlob(body string, dirOnly bool) *regexp.Regexp {
	// A directory pattern made only of leading `**/` runs (`/**/`, `**/**/`) is all `ignore`'s leading-`**/` replacer, `^(?:.*\/)?`: it matches every directory.
	if dirOnly && strings.ReplaceAll(body+"/", "**/", "") == "" {
		return matchesEverything()
	}
	var tokens []globToken
	for {
		rest, ok := strings.CutPrefix(body, "**/")
		if !ok || rest == "" {
			break
		}
		if len(tokens) == 0 {
			tokens = append(tokens, globToken{kind: 'g', source: "(?:.*/)?"})
		}
		body = rest
	}
	for i := 0; i < len(body); {
		c := body[i]
		switch {
		case c == '/' && strings.HasPrefix(body[i:], "/**") && (i+3 == len(body) || body[i+3] == '/'):
			if i+3 == len(body) {
				tokens = append(tokens, globToken{kind: 'g', source: "/.+"})
			} else {
				tokens = append(tokens, globToken{kind: 'g', source: "(?:/[^/]+)*"})
			}
			i += 3
		case c == '*':
			run := i
			for i < len(body) && body[i] == '*' {
				i++
			}
			tokens = append(tokens, globToken{kind: '*', char: body[run:i]})
		case c == '?':
			tokens = append(tokens, globToken{kind: '?', source: "[^/]"})
			i++
		case c == '[':
			class, end, ok := bracketClass(body, i)
			if !ok {
				return neverMatches()
			}
			tokens = append(tokens, globToken{kind: 'c', source: class})
			i = end
		case c == '\\' && i+1 < len(body):
			_, size := utf8.DecodeRuneInString(body[i+1:])
			tokens = append(tokens, globToken{kind: 'l', char: body[i+1 : i+1+size], source: regexp.QuoteMeta(body[i+1 : i+1+size])})
			i += 1 + size
		default:
			_, size := utf8.DecodeRuneInString(body[i:])
			tokens = append(tokens, globToken{kind: 'l', char: body[i : i+size], source: regexp.QuoteMeta(body[i : i+size])})
			i += size
		}
	}
	// A star that ends the pattern is not part of a run: it matches the rest of a name, and at least one character after a slash.
	// A pattern that ends in a slash keeps its last star in the run.
	// Only a single `*` is that trailing wildcard: a trailing `**` stays the last wildcard of its run, so the stars before it are pinned (`*.ts**` does not match test.spec.ts).
	if last := len(tokens) - 1; !dirOnly && last >= 0 && tokens[last].kind == '*' && tokens[last].char == "*" {
		tokens[last].kind = 't'
		tokens[last].source = "[^/]*"
		if last > 0 && tokens[last-1].kind == 'l' && tokens[last-1].char == "/" {
			tokens[last].source = "[^/]+"
		}
	}
	var re strings.Builder
	re.WriteString("(?i)^")
	runStart := 0
	for index := 0; index <= len(tokens); index++ {
		if index < len(tokens) && tokens[index].kind != 'g' {
			continue
		}
		lastStar := -1
		for j := runStart; j < index; j++ {
			if tokens[j].kind == '*' {
				lastStar = j
			}
		}
		for j := runStart; j < index; j++ {
			token := tokens[j]
			switch {
			case token.kind != '*':
				re.WriteString(token.source)
			case j == lastStar:
				re.WriteString("[^/]*")
			default:
				switch next := tokens[j+1]; next.kind {
				case 'l':
					re.WriteString("[^/" + next.source + "]*")
				case '?':
				default:
					re.WriteString("[^/]*")
				}
			}
		}
		if index < len(tokens) {
			re.WriteString(tokens[index].source)
		}
		runStart = index + 1
	}
	re.WriteString("$")
	compiled, err := regexp.Compile(re.String())
	if err != nil {
		return nil
	}
	return compiled
}

// The two constant matchers compile on first use, so linking the package compiles no expression at init.
var (
	neverMatches      = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(`[^\x00-\x{10FFFF}]`) })
	matchesEverything = sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(``) })
)

// bracketClass ports scanBracket of the `ignore` package (git's wildmatch.c member loop): the first member is taken unconditionally (so a leading `]` is a member),
// `\` escapes the next member, an out-of-order range keeps only its lower bound, `[:name:]` names one of git's ASCII POSIX classes, and a bracket expression never
// matches `/`. It returns the index after the expression; an unterminated expression or an unknown class name makes the whole pattern match nothing.
func bracketClass(pattern string, start int) (string, int, bool) {
	i := start + 1
	negated := false
	if i < len(pattern) && (pattern[i] == '!' || pattern[i] == '^') {
		negated = true
		i++
	}
	var members [][2]rune
	var prev rune
	hasPrev := false
	for {
		if i >= len(pattern) {
			return "", 0, false
		}
		c, size := utf8.DecodeRuneInString(pattern[i:])
		switch {
		case c == '\\':
			if i+size >= len(pattern) {
				return "", 0, false
			}
			escaped, escapedSize := utf8.DecodeRuneInString(pattern[i+size:])
			members = append(members, [2]rune{escaped, escaped})
			prev, hasPrev = escaped, true
			size += escapedSize
		case c == '-' && hasPrev && i+1 < len(pattern) && pattern[i+1] != ']':
			next := i + 1
			to, toSize := utf8.DecodeRuneInString(pattern[next:])
			if to == '\\' {
				next += toSize
				if next >= len(pattern) {
					return "", 0, false
				}
				to, toSize = utf8.DecodeRuneInString(pattern[next:])
			}
			if prev <= to {
				members = append(members, [2]rune{prev, to})
			}
			hasPrev = false
			size = next + toSize - i
		case c == '[' && i+1 < len(pattern) && pattern[i+1] == ':':
			nameStart := i + 2
			end := nameStart
			for end < len(pattern) && pattern[end] != ']' {
				end++
			}
			if end == len(pattern) {
				return "", 0, false
			}
			if end > nameStart && pattern[end-1] == ':' {
				class, ok := posixClasses[pattern[nameStart:end-1]]
				if !ok {
					return "", 0, false
				}
				members = append(members, class...)
				hasPrev = false
				size = end + 1 - i
			} else {
				members = append(members, [2]rune{'[', '['})
				prev, hasPrev = '[', true
			}
		default:
			members = append(members, [2]rune{c, c})
			prev, hasPrev = c, true
		}
		i += size
		if i < len(pattern) && pattern[i] == ']' {
			return classSource(negated, members), i + 1, true
		}
	}
}

// posixClasses are git's ASCII-only ctype classes as the `ignore` package expands them (POSIX_CLASSES); `/` is in none of them.
var posixClasses = map[string][][2]rune{
	"alnum":  {{'0', '9'}, {'A', 'Z'}, {'a', 'z'}},
	"alpha":  {{'A', 'Z'}, {'a', 'z'}},
	"blank":  {{' ', ' '}, {'\t', '\t'}},
	"cntrl":  {{0x00, 0x1f}, {0x7f, 0x7f}},
	"digit":  {{'0', '9'}},
	"graph":  {{'!', '.'}, {'0', '~'}},
	"lower":  {{'a', 'z'}},
	"print":  {{' ', '.'}, {'0', '~'}},
	"punct":  {{'!', '.'}, {':', '@'}, {'[', '`'}, {'{', '~'}},
	"space":  {{' ', ' '}, {'\t', '\t'}, {'\n', '\n'}, {'\r', '\r'}},
	"upper":  {{'A', 'Z'}},
	"xdigit": {{'0', '9'}, {'A', 'F'}, {'a', 'f'}},
}

// classSource writes members as a character class that never matches `/`: a negated class excludes it, and a plain one has it removed from its ranges.
func classSource(negated bool, members [][2]rune) string {
	var class strings.Builder
	class.WriteString("[")
	if negated {
		class.WriteString(`^/`)
	}
	written := 0
	write := func(lo, hi rune) {
		if lo > hi {
			return
		}
		fmt.Fprintf(&class, `\x{%x}`, lo)
		if hi != lo {
			fmt.Fprintf(&class, `-\x{%x}`, hi)
		}
		written++
	}
	for _, member := range members {
		if !negated && member[0] <= '/' && '/' <= member[1] {
			write(member[0], '/'-1)
			write('/'+1, member[1])
			continue
		}
		write(member[0], member[1])
	}
	if written == 0 && !negated {
		return neverMatches().String()
	}
	class.WriteString("]")
	return class.String()
}

func (rule Rule) match(candidate string, directory bool) bool {
	if rule.matcher == nil || rule.dirOnly && !directory {
		return false
	}
	if rule.basenameOnly {
		if slash := strings.LastIndexByte(candidate, '/'); slash >= 0 {
			candidate = candidate[slash+1:]
		}
	}
	return rule.matcher.MatchString(candidate)
}
