package codingagent

import (
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/yuin/goldmark/ast"
	gmparser "github.com/yuin/goldmark/parser"
	gmtext "github.com/yuin/goldmark/text"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Ports packages/coding-agent/src/modes/interactive/components/mermaid.ts (Marked heading ownership before top-level code-token selection).
// Marked's ATX rule uses JavaScript whitespace after the marker and dot for its line content. Heading text is not rendered here; only block ownership matters.
type markedATXHeadingParser struct{}

func (*markedATXHeadingParser) Trigger() []byte                                 { return []byte{'#'} }
func (*markedATXHeadingParser) CanInterruptParagraph() bool                     { return true }
func (*markedATXHeadingParser) CanAcceptIndentedLine() bool                     { return false }
func (*markedATXHeadingParser) Close(ast.Node, gmtext.Reader, gmparser.Context) {}
func (*markedATXHeadingParser) Continue(ast.Node, gmtext.Reader, gmparser.Context) gmparser.State {
	return gmparser.Close
}

func (*markedATXHeadingParser) Open(_ ast.Node, reader gmtext.Reader, pc gmparser.Context) (ast.Node, gmparser.State) {
	line, segment := reader.PeekLine()
	pos := pc.BlockOffset()
	if pos < 0 || pos >= len(line) {
		return nil, gmparser.NoChildren
	}
	value := strings.TrimSuffix(string(line[pos:]), "\n")
	level := len(value) - len(strings.TrimLeft(value, "#"))
	if level == 0 || level > 6 {
		return nil, gmparser.NoChildren
	}
	rest := value[level:]
	if rest != "" {
		first, _ := utf8.DecodeRuneInString(rest)
		if !widthx.IsJSSpace(first) || strings.ContainsAny(rest, "\u2028\u2029") {
			return nil, gmparser.NoChildren
		}
	}
	node := ast.NewHeading(level)
	node.Lines().Append(segment)
	reader.AdvanceToEOL()
	return node, gmparser.NoChildren
}

var markedSetextBar = lazyregexp.New(`^ {0,3}(?:=+|-+) *$`)

// Marked's GFM setext rule excludes table-looking header/delimiter pairs before the table tokenizer validates column counts.
var markedSetextTableDelimiter = lazyregexp.New(`^ {0,3}\|?(?:[:\- ]*\|)+[:\- ]*$`)

type markedSetextHeadingParser struct{ gmparser.BlockParser }

func (p *markedSetextHeadingParser) Open(parent ast.Node, reader gmtext.Reader, pc gmparser.Context) (ast.Node, gmparser.State) {
	line, _ := reader.PeekLine()
	if !markedSetextBar.MatchString(strings.TrimSuffix(string(line), "\n")) {
		return nil, gmparser.NoChildren
	}
	if paragraph, ok := pc.LastOpenedBlock().Node.(*ast.Paragraph); ok {
		for i := range paragraph.Lines().Len() {
			segment := paragraph.Lines().At(i)
			value := string(segment.Value(reader.Source()))
			if strings.ContainsAny(value, "\u2028\u2029") || (i > 0 && markedSetextTableDelimiter.MatchString(strings.TrimSuffix(value, "\n"))) {
				return nil, gmparser.NoChildren
			}
		}
	}
	return p.BlockParser.Open(parent, reader, pc)
}
