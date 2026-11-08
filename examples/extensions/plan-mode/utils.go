package planmode

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

const jsWhitespace = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

var (
	destructivePatterns = compileCommandPatterns([]string{
		`(?i)\brm\b`,
		`(?i)\brmdir\b`,
		`(?i)\bmv\b`,
		`(?i)\bcp\b`,
		`(?i)\bmkdir\b`,
		`(?i)\btouch\b`,
		`(?i)\bchmod\b`,
		`(?i)\bchown\b`,
		`(?i)\bchgrp\b`,
		`(?i)\bln\b`,
		`(?i)\btee\b`,
		`(?i)\btruncate\b`,
		`(?i)\bdd\b`,
		`(?i)\bshred\b`,
		`(?i)\bnpm\s+(install|uninstall|update|ci|link|publish)`,
		`(?i)\byarn\s+(add|remove|install|publish)`,
		`(?i)\bpnpm\s+(add|remove|install|publish)`,
		`(?i)\bpip\s+(install|uninstall)`,
		`(?i)\bapt(-get)?\s+(install|remove|purge|update|upgrade)`,
		`(?i)\bbrew\s+(install|uninstall|upgrade)`,
		`(?i)\bgit\s+(add|commit|push|pull|merge|rebase|reset|checkout|branch\s+-[dD]|stash|cherry-pick|revert|tag|init|clone)`,
		`(?i)\bsudo\b`,
		`(?i)\bsu\b`,
		`(?i)\bkill\b`,
		`(?i)\bpkill\b`,
		`(?i)\bkillall\b`,
		`(?i)\breboot\b`,
		`(?i)\bshutdown\b`,
		`(?i)\bsystemctl\s+(start|stop|restart|enable|disable)`,
		`(?i)\bservice\s+\S+\s+(start|stop|restart)`,
		`(?i)\b(vim?|nano|emacs|code|subl)\b`,
	})
	safePatterns = compileCommandPatterns([]string{
		`^\s*cat\b`,
		`^\s*head\b`,
		`^\s*tail\b`,
		`^\s*less\b`,
		`^\s*more\b`,
		`^\s*grep\b`,
		`^\s*find\b`,
		`^\s*ls\b`,
		`^\s*pwd\b`,
		`^\s*echo\b`,
		`^\s*printf\b`,
		`^\s*wc\b`,
		`^\s*sort\b`,
		`^\s*uniq\b`,
		`^\s*diff\b`,
		`^\s*file\b`,
		`^\s*stat\b`,
		`^\s*du\b`,
		`^\s*df\b`,
		`^\s*tree\b`,
		`^\s*which\b`,
		`^\s*whereis\b`,
		`^\s*type\b`,
		`^\s*env\b`,
		`^\s*printenv\b`,
		`^\s*uname\b`,
		`^\s*whoami\b`,
		`^\s*id\b`,
		`^\s*date\b`,
		`^\s*cal\b`,
		`^\s*uptime\b`,
		`^\s*ps\b`,
		`^\s*top\b`,
		`^\s*htop\b`,
		`^\s*free\b`,
		`(?i)^\s*git\s+(status|log|diff|show|branch|remote|config\s+--get)`,
		`(?i)^\s*git\s+ls-`,
		`(?i)^\s*npm\s+(list|ls|view|info|search|outdated|audit)`,
		`(?i)^\s*yarn\s+(list|info|why|audit)`,
		`(?i)^\s*node\s+--version`,
		`(?i)^\s*python\s+--version`,
		`(?i)^\s*curl\s`,
		`(?i)^\s*wget\s+-O\s*-`,
		`^\s*jq\b`,
		`(?i)^\s*sed\s+-n`,
		`^\s*awk\b`,
		`^\s*rg\b`,
		`^\s*fd\b`,
		`^\s*bat\b`,
		`^\s*eza\b`,
	})
	markdownEmphasisPattern = compilePlanPattern(`\*{1,2}([^*]+)\*{1,2}`)
	markdownCodePattern     = compilePlanPattern("`([^`]+)`")
	leadingActionPattern    = compilePlanPattern(`(?i)^(Use|Run|Execute|Create|Write|Read|Check|Verify|Update|Modify|Add|Remove|Delete|Install)\s+(the\s+)?`)
	whitespacePattern       = compilePlanPattern(`\s+`)
	planHeaderPattern       = compilePlanPattern(`(?i)\*{0,2}Plan:\*{0,2}\s*\n`)
	numberedStepPattern     = compilePlanPattern(`(?:^|[\r\n\x{2028}\x{2029}])\s*(\d+)[.)]\s+([^\r\n\x{2028}\x{2029}]+)`)
	trailingEmphasisPattern = compilePlanPattern(`\*{1,2}$`)
	doneBracketPattern      = compilePlanPattern(`(?i)\[DONE\s*:?\s*(\d+)\]`)
	doneColonPattern        = compilePlanPattern(`(?i)\bDone\s*:\s*(\d+)\b`)
	doneProsePattern        = compilePlanPattern(`(?i)\bStep\s*(\d+)\s+(?:is\s+)?(?:done|complete|completed|finished)\b`)
	wholePlanAllPattern     = compilePlanPattern(`(?i)(?:all|every)\s+steps?\s+(?:are|is\s+)?(?:done|complete|completed|finished)`)
	wholePlanPlanPattern    = compilePlanPattern(`(?i)\b(?:the\s+)?plan\s+(?:is\s+)?(?:complete|completed|done)\b`)
)

func compileCommandPatterns(sources []string) []*regexp.Regexp {
	patterns := make([]*regexp.Regexp, 0, len(sources))
	for _, source := range sources {
		patterns = append(patterns, compilePlanPattern(source))
	}
	return patterns
}

// The plan patterns use ASCII literals, ASCII word boundaries, and JavaScript whitespace. Expand their /i literals without the non-ASCII folds Go's (?i) adds; the sole alphabetic class already spells both cases ([dD]).
func compilePlanPattern(source string) *regexp.Regexp {
	if rest, insensitive := strings.CutPrefix(source, "(?i)"); insensitive {
		var expanded strings.Builder
		inClass := false
		for i := 0; i < len(rest); i++ {
			char := rest[i]
			if char == '\\' && i+1 < len(rest) {
				expanded.WriteString(rest[i : i+2])
				i++
				continue
			}
			switch char {
			case '[':
				inClass = true
			case ']':
				inClass = false
			}
			if !inClass && (char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z') {
				lower := char | 0x20
				expanded.Write([]byte{'[', lower, lower - ('a' - 'A'), ']'})
			} else {
				expanded.WriteByte(char)
			}
		}
		source = expanded.String()
	}
	source = strings.NewReplacer(`\s`, "["+jsWhitespace+"]", `\S`, "[^"+jsWhitespace+"]").Replace(source)
	return regexp.MustCompile(source)
}

func jsTrim(text string) string {
	return strings.Trim(text, jsWhitespace)
}

// TodoItem is one numbered plan step and its completion state.
type TodoItem struct {
	Step      int    `json:"step"`
	Text      string `json:"text"`
	Completed bool   `json:"completed"`
}

// IsSafeCommand reports whether command starts with an allowlisted read-only command and contains none of plan mode's destructive patterns.
func IsSafeCommand(command string) bool {
	isDestructive := hasUnquotedOutputRedirect(command)
	for _, pattern := range destructivePatterns {
		if pattern.MatchString(command) {
			isDestructive = true
			break
		}
	}
	isSafe := false
	for _, pattern := range safePatterns {
		if pattern.MatchString(command) {
			isSafe = true
			break
		}
	}
	return !isDestructive && isSafe
}

func hasUnquotedOutputRedirect(command string) bool {
	for i, char := range command {
		if char != '>' {
			continue
		}
		if i == 0 || command[i-1] != '<' {
			return true
		}
	}
	return false
}

// CleanStepText removes lightweight Markdown and leading action words, then applies JavaScript whitespace and first-code-unit uppercase semantics. It keeps the full text; the widget truncates for display.
func CleanStepText(text string) string {
	cleaned := markdownEmphasisPattern.ReplaceAllString(text, "$1")
	cleaned = markdownCodePattern.ReplaceAllString(cleaned, "$1")
	cleaned = leadingActionPattern.ReplaceAllString(cleaned, "")
	cleaned = jsTrim(whitespacePattern.ReplaceAllString(cleaned, " "))
	cleaned = capitalizeFirstCodeUnit(cleaned)
	return cleaned
}

func capitalizeFirstCodeUnit(text string) string {
	first, size := utf8.DecodeRuneInString(text)
	if size == 0 || first == utf8.RuneError || first > 0xffff {
		return text
	}
	return cases.Upper(language.Und).String(string(first)) + text[size:]
}

func utf16Length(text string) int {
	return len(utf16.Encode([]rune(text)))
}

func utf16Slice(text string, end int) string {
	units := utf16.Encode([]rune(text))
	return string(utf16.Decode(units[:min(end, len(units))]))
}

// ExtractTodoItems returns valid numbered steps under the last Plan header, so a draft in a thinking block does not duplicate the plan in the text block.
func ExtractTodoItems(message string) []TodoItem {
	headers := planHeaderPattern.FindAllStringIndex(message, -1)
	if len(headers) == 0 {
		return []TodoItem{}
	}
	header := headers[len(headers)-1]

	matches := numberedStepPattern.FindAllStringSubmatch(message[header[1]:], -1)
	items := make([]TodoItem, 0, len(matches))
	for _, match := range matches {
		text := jsTrim(match[2])
		text = jsTrim(trailingEmphasisPattern.ReplaceAllString(text, ""))
		if utf16Length(text) <= 5 || strings.HasPrefix(text, "/") || strings.HasPrefix(text, "-") {
			continue
		}
		cleaned := CleanStepText(text)
		if utf16Length(cleaned) > 3 {
			items = append(items, TodoItem{Step: len(items) + 1, Text: cleaned})
		}
	}
	return items
}

// ExtractDoneSteps returns the distinct JavaScript Numbers named by DONE markers ([DONE:1], [DONE: 1], [DONE 1]), "Done: 1" and "Step 1 done / is complete / finished", in the order of those three forms. Phrases are adjacency-anchored, so "Step 9 is not done" never matches.
func ExtractDoneSteps(message string) []float64 {
	var steps []float64
	seen := map[float64]bool{}
	for _, pattern := range []*regexp.Regexp{doneBracketPattern, doneColonPattern, doneProsePattern} {
		for _, match := range pattern.FindAllStringSubmatch(message, -1) {
			value, _ := strconv.ParseFloat(match[1], 64) // out of range is +Inf, as Number(...)
			if !seen[value] {
				seen[value] = true
				steps = append(steps, value)
			}
		}
	}
	if steps == nil {
		steps = []float64{}
	}
	return steps
}

// MarkCompletedSteps applies DONE markers to matching incomplete items and returns the number of items it newly completed.
func MarkCompletedSteps(text string, items []TodoItem) int {
	marked := 0
	for _, step := range ExtractDoneSteps(text) {
		for i := range items {
			if float64(items[i].Step) == step && !items[i].Completed {
				items[i].Completed = true
				marked++
				break
			}
		}
	}
	return marked
}

// AllStepsComplete reports whole-plan completion phrasing ("All steps complete", "The plan is done"), the fallback when the model summarizes without DONE tags. Consulted on the run's final message only.
func AllStepsComplete(message string) bool {
	return wholePlanAllPattern.MatchString(message) || wholePlanPlanPattern.MatchString(message)
}
