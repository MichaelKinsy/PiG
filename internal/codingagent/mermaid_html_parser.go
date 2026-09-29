package codingagent

// Ports packages/coding-agent/src/modes/interactive/components/mermaid.ts (Marked HTML block ownership).

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/yuin/goldmark/ast"
	gmparser "github.com/yuin/goldmark/parser"
	gmtext "github.com/yuin/goldmark/text"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Marked's HTML rules use ASCII case-insensitive tag names, space-only attribute separators, and matching raw-text closing tags.
const markedJSSpace = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

var markedBlockTag = lazyregexp.New(`^</?(?:address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h[1-6]|head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|meta|nav|noframes|ol|optgroup|option|p|param|search|section|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul)(?: +|\n|/?>)`)
var markedGenericTag = lazyregexp.New(`^<([a-z][\w-]*)(?: +[a-z:_][\w.:-]*(?: *= *"[^"\n]*"| *= *'[^'\n]*'| *= *[^` + markedJSSpace + `"'=<>` + "`" + `]+)?)*? */?>[ \t]*(?:\n|$)`)
var markedGenericClose = lazyregexp.New(`^</([a-zA-Z][\w-]*)[` + markedJSSpace + `]*>[ \t]*(?:\n|$)`)

type markedHTMLBlock struct {
	ast.HTMLBlock
	closing string
	ended   bool
}

type markedHTMLParser struct{}

func (*markedHTMLParser) Trigger() []byte                                 { return []byte{'<'} }
func (*markedHTMLParser) CanInterruptParagraph() bool                     { return true }
func (*markedHTMLParser) CanAcceptIndentedLine() bool                     { return false }
func (*markedHTMLParser) Close(ast.Node, gmtext.Reader, gmparser.Context) {}

func asciiLower(text string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, text)
}

func (*markedHTMLParser) Open(_ ast.Node, reader gmtext.Reader, pc gmparser.Context) (ast.Node, gmparser.State) {
	line, segment := reader.PeekLine()
	original := string(line)
	trimmed := strings.TrimLeft(original, " ")
	if len(original)-len(trimmed) > 3 {
		return nil, gmparser.NoChildren
	}
	lower := asciiLower(trimmed)
	closing, recognized, ended := "", false, false
	for _, tag := range []string{"script", "pre", "style", "textarea"} {
		if rest, ok := strings.CutPrefix(lower, "<"+tag); ok && len(rest) > 0 {
			first := []rune(rest)[0]
			if first == '>' || widthx.IsJSSpace(first) {
				closing, recognized = "</"+tag+">", true
				break
			}
		}
	}
	if !recognized {
		switch {
		case strings.HasPrefix(lower, "<!--"):
			closing, recognized = "-->", true
			ended = strings.HasPrefix(lower, "<!-->") || strings.HasPrefix(lower, "<!--->")
		case strings.HasPrefix(lower, "<?"):
			closing, recognized = "?>", true
		case strings.HasPrefix(lower, "<![cdata["):
			closing, recognized = "]]>", true
		case len(lower) > 2 && strings.HasPrefix(lower, "<!") && lower[2] >= 'a' && lower[2] <= 'z':
			closing, recognized = ">", true
		case markedBlockTag.MatchString(lower):
			recognized = true
		default:
			name := ""
			if match := markedGenericTag.FindStringSubmatch(lower); match != nil {
				name = match[1]
			} else {
				start := segment.Start - segment.Padding + len(original) - len(trimmed)
				if match := markedGenericClose.FindSubmatch(reader.Source()[start:]); match != nil {
					name = asciiLower(string(match[1]))
				}
			}
			if name != "" && !ast.IsParagraph(pc.LastOpenedBlock().Node) && !strings.HasPrefix(name, "script") && !strings.HasPrefix(name, "pre") && !strings.HasPrefix(name, "style") && !strings.HasPrefix(name, "textarea") {
				recognized = true
			}
		}
	}
	if !recognized {
		return nil, gmparser.NoChildren
	}
	node := &markedHTMLBlock{HTMLBlock: *ast.NewHTMLBlock(ast.HTMLBlockType6), closing: closing, ended: ended || closing != "" && strings.Contains(lower, closing)}
	node.Lines().Append(segment)
	reader.AdvanceToEOL()
	return node, gmparser.NoChildren
}

func (*markedHTMLParser) Continue(node ast.Node, reader gmtext.Reader, _ gmparser.Context) gmparser.State {
	block := node.(*markedHTMLBlock)
	if block.ended {
		return gmparser.Close
	}
	line, segment := reader.PeekLine()
	if block.closing == "" && strings.Trim(string(line), " \t\n") == "" {
		return gmparser.Close
	}
	block.Lines().Append(segment)
	reader.AdvanceToEOL()
	if block.closing != "" && strings.Contains(asciiLower(string(line)), block.closing) {
		return gmparser.Close
	}
	return gmparser.Continue | gmparser.NoChildren
}
